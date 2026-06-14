package osrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient returns a Client pointing at the given test server URL.
func newTestClient(serverURL string) *Client {
	cfg := DefaultConfig()
	cfg.BaseURL = serverURL
	cfg.Rate = 0 // no pacing in tests
	cfg.Retries = 1
	return NewClient(cfg)
}

// TestRouteBasic checks that Route parses a valid OSRM route response.
func TestRouteBasic(t *testing.T) {
	payload := wireRouteResp{
		Code: "Ok",
		Routes: []wireRoute{
			{Distance: 342500, Duration: 12180, Legs: []wireLeg{{Distance: 342500, Duration: 12180}}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	routes, err := c.Route(context.Background(), []string{"51.5074,-0.1276", "48.8566,2.3522"}, "driving")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	r := routes[0]
	if r.Index != 0 {
		t.Errorf("Index = %d, want 0", r.Index)
	}
	// 342500 m / 1000 = 342.5 km
	if r.Distance < 342 || r.Distance > 343 {
		t.Errorf("Distance = %.2f km, want ~342.5", r.Distance)
	}
	// 12180 s / 60 = 203 min
	if r.Duration < 202 || r.Duration > 204 {
		t.Errorf("Duration = %.2f min, want ~203", r.Duration)
	}
	if r.Legs != 1 {
		t.Errorf("Legs = %d, want 1", r.Legs)
	}
}

// TestNearestBasic checks that Nearest parses a valid OSRM nearest response.
func TestNearestBasic(t *testing.T) {
	payload := wireNearestResp{
		Code: "Ok",
		Waypoints: []wireWaypoint{
			{Name: "Whitehall", Distance: 1.5, Location: []float64{-0.127607, 51.5074}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	pts, err := c.Nearest(context.Background(), "51.5074,-0.1276", 1, "driving")
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	p := pts[0]
	if p.Name != "Whitehall" {
		t.Errorf("Name = %q, want Whitehall", p.Name)
	}
	if p.Distance != 1.5 {
		t.Errorf("Distance = %.1f, want 1.5", p.Distance)
	}
	if p.Lon != -0.127607 {
		t.Errorf("Lon = %f, want -0.127607", p.Lon)
	}
	if p.Lat != 51.5074 {
		t.Errorf("Lat = %f, want 51.5074", p.Lat)
	}
}

// TestTableBasic checks that Table parses a valid OSRM table response.
func TestTableBasic(t *testing.T) {
	payload := wireTableResp{
		Code:      "Ok",
		Durations: [][]float64{{0, 4200}, {4500, 0}},
		Distances: [][]float64{{0, 350000}, {340000, 0}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	rows, err := c.Table(context.Background(), []string{"51.5074,-0.1276", "48.8566,2.3522"}, "driving")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	// 2x2 matrix = 4 rows
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	// diagonal should be 0
	for _, row := range rows {
		if row.From == row.To {
			if row.Distance != 0 || row.Duration != 0 {
				t.Errorf("diagonal row [%d][%d] not zero: dist=%.2f dur=%.2f",
					row.From, row.To, row.Distance, row.Duration)
			}
		}
	}
	// off-diagonal should be non-zero
	for _, row := range rows {
		if row.From != row.To {
			if row.Distance == 0 || row.Duration == 0 {
				t.Errorf("off-diagonal row [%d][%d] is zero", row.From, row.To)
			}
		}
	}
}

// TestParseCoord checks that parseCoord swaps lat,lon to lon,lat.
func TestParseCoord(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"51.5074,-0.1276", "-0.1276,51.5074"},
		{"48.8566,2.3522", "2.3522,48.8566"},
		{"37.8,-122.4", "-122.4,37.8"},
	}
	for _, tc := range cases {
		got, err := parseCoord(tc.input)
		if err != nil {
			t.Errorf("parseCoord(%q) error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseCoord(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestParseCoordInvalid checks that bad input returns an error.
func TestParseCoordInvalid(t *testing.T) {
	_, err := parseCoord("notacoord")
	if err == nil {
		t.Error("parseCoord(invalid) should return error")
	}
}

// TestGetRetriesOn503 verifies that the client retries on 5xx errors.
func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(wireRouteResp{
			Code:   "Ok",
			Routes: []wireRoute{{Distance: 1000, Duration: 60, Legs: []wireLeg{{}}}},
		})
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 5
	c := NewClient(cfg)

	_, err := c.get(context.Background(), srv.URL+"/route/v1/driving/0,0;1,1?overview=false&steps=false")
	if err != nil {
		t.Fatalf("get with retries: %v", err)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
}

// TestTableRequiresMinTwo checks that Table with fewer than 2 coords returns error.
func TestTableRequiresMinTwo(t *testing.T) {
	c := newTestClient("http://localhost:0")
	_, err := c.Table(context.Background(), []string{"51.5074,-0.1276"}, "driving")
	if err == nil {
		t.Error("Table with 1 coord should return error")
	}
}
