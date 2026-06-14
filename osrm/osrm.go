// Package osrm is the library behind the osrm command line:
// the HTTP client, request shaping, and the typed data models for the
// OSRM (Open Source Routing Machine) demo server (router.project-osrm.org).
//
// The client is polite: it sets a real User-Agent, paces requests, and
// retries transient failures (429 and 5xx). OSRM uses (longitude, latitude)
// coordinate order in URLs; the public API accepts the more natural (lat, lon)
// and swaps internally.
package osrm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Host is the OSRM demo server hostname.
const Host = "router.project-osrm.org"

// BaseURL is the root every request is built from.
const BaseURL = "http://" + Host

// DefaultUserAgent identifies the client. Honest, descriptive, contact included.
const DefaultUserAgent = "osrm-cli/0.1 (tamnd87@gmail.com)"

// Config holds all tunable parameters for the Client.
type Config struct {
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Timeout   time.Duration
	Retries   int
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		BaseURL:   BaseURL,
		UserAgent: DefaultUserAgent,
		Rate:      200 * time.Millisecond,
		Timeout:   30 * time.Second,
		Retries:   3,
	}
}

// Client talks to the OSRM demo server over HTTP.
type Client struct {
	cfg  Config
	http *http.Client
	last time.Time
}

// NewClient returns a Client configured with cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// Route finds the driving (or cycling/walking) route between two or more
// waypoints. coords is a slice of "lat,lon" strings; mode is "driving",
// "cycling", or "walking".
func (c *Client) Route(ctx context.Context, coords []string, mode string) ([]Route, error) {
	osrmCoords, err := parseCoords(coords)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/route/v1/%s/%s?overview=false&steps=false",
		c.cfg.BaseURL, mode, strings.Join(osrmCoords, ";"))
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var resp wireRouteResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode route response: %w", err)
	}
	if resp.Code != "Ok" {
		return nil, fmt.Errorf("osrm error: %s", resp.Code)
	}
	out := make([]Route, 0, len(resp.Routes))
	for i, r := range resp.Routes {
		out = append(out, Route{
			Index:    i,
			Distance: r.Distance / 1000,
			Duration: r.Duration / 60,
			Legs:     len(r.Legs),
		})
	}
	return out, nil
}

// Nearest finds the nearest road segment(s) to a single "lat,lon" coordinate.
// count controls how many candidates are returned (default 1).
func (c *Client) Nearest(ctx context.Context, coord string, count int, mode string) ([]NearestPoint, error) {
	osrmCoord, err := parseCoord(coord)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 1
	}
	url := fmt.Sprintf("%s/nearest/v1/%s/%s?number=%d",
		c.cfg.BaseURL, mode, osrmCoord, count)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var resp wireNearestResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode nearest response: %w", err)
	}
	if resp.Code != "Ok" {
		return nil, fmt.Errorf("osrm error: %s", resp.Code)
	}
	out := make([]NearestPoint, 0, len(resp.Waypoints))
	for _, wp := range resp.Waypoints {
		name := wp.Name
		if name == "" {
			name = fmt.Sprintf("%.6f,%.6f", wp.Location[1], wp.Location[0])
		}
		out = append(out, NearestPoint{
			Name:     name,
			Distance: wp.Distance,
			Lon:      wp.Location[0],
			Lat:      wp.Location[1],
		})
	}
	return out, nil
}

// Table computes the distance and duration matrix between all provided
// "lat,lon" coordinates. coords must have at least 2 entries.
func (c *Client) Table(ctx context.Context, coords []string, mode string) ([]MatrixRow, error) {
	if len(coords) < 2 {
		return nil, fmt.Errorf("table requires at least 2 coordinates")
	}
	osrmCoords, err := parseCoords(coords)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/table/v1/%s/%s?annotations=distance,duration",
		c.cfg.BaseURL, mode, strings.Join(osrmCoords, ";"))
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var resp wireTableResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode table response: %w", err)
	}
	if resp.Code != "Ok" {
		return nil, fmt.Errorf("osrm error: %s", resp.Code)
	}
	n := len(coords)
	var out []MatrixRow
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			var dist, dur float64
			if i < len(resp.Distances) && j < len(resp.Distances[i]) {
				dist = resp.Distances[i][j] / 1000
			}
			if i < len(resp.Durations) && j < len(resp.Durations[i]) {
				dur = resp.Durations[i][j] / 60
			}
			out = append(out, MatrixRow{
				From:     i,
				To:       j,
				Distance: dist,
				Duration: dur,
			})
		}
	}
	return out, nil
}

// get fetches a URL with pacing and retries. The caller owns nothing;
// the body is read fully and closed here.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", url, lastErr)
}

func (c *Client) do(ctx context.Context, url string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.cfg.Rate <= 0 {
		return
	}
	if wait := c.cfg.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- coordinate helpers ---

// parseCoord parses a "lat,lon" string into the "lon,lat" OSRM URL format.
func parseCoord(s string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ",", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("expected lat,lon got %q", s)
	}
	lat := strings.TrimSpace(parts[0])
	lon := strings.TrimSpace(parts[1])
	return lon + "," + lat, nil // OSRM needs lon,lat
}

// parseCoords parses a slice of "lat,lon" strings into OSRM "lon,lat" format.
func parseCoords(coords []string) ([]string, error) {
	out := make([]string, 0, len(coords))
	for _, c := range coords {
		osrm, err := parseCoord(c)
		if err != nil {
			return nil, err
		}
		out = append(out, osrm)
	}
	return out, nil
}

// --- output types ---

// Route is one computed route between two or more waypoints.
type Route struct {
	Index    int     `kit:"id"      json:"index"`
	Distance float64 `json:"distance_km"`
	Duration float64 `json:"duration_min"`
	Legs     int     `json:"legs"`
}

// NearestPoint is the nearest road point to a queried coordinate.
type NearestPoint struct {
	Name     string  `kit:"id" json:"name"`
	Distance float64 `json:"distance_m"`
	Lon      float64 `json:"lon"`
	Lat      float64 `json:"lat"`
}

// MatrixRow is one cell in the distance/duration matrix.
type MatrixRow struct {
	From     int     `kit:"id" json:"from"`
	To       int     `json:"to"`
	Distance float64 `json:"distance_km"`
	Duration float64 `json:"duration_min"`
}

// --- wire types (OSRM JSON shapes) ---

type wireRouteResp struct {
	Code   string      `json:"code"`
	Routes []wireRoute `json:"routes"`
}

type wireRoute struct {
	Distance float64   `json:"distance"` // meters
	Duration float64   `json:"duration"` // seconds
	Legs     []wireLeg `json:"legs"`
}

type wireLeg struct {
	Distance float64 `json:"distance"`
	Duration float64 `json:"duration"`
}

type wireNearestResp struct {
	Code      string          `json:"code"`
	Waypoints []wireWaypoint  `json:"waypoints"`
}

type wireWaypoint struct {
	Name     string    `json:"name"`
	Distance float64   `json:"distance"`
	Location []float64 `json:"location"` // [lon, lat]
}

type wireTableResp struct {
	Code      string      `json:"code"`
	Durations [][]float64 `json:"durations"`
	Distances [][]float64 `json:"distances"`
}
