// Package osrm exposes the OSRM (Open Source Routing Machine) demo server as a
// kit Domain driver. A multi-domain host (ant) enables it with a single blank
// import:
//
//	import _ "github.com/tamnd/osrm-cli/osrm"
//
// The same Domain also builds the standalone osrm binary, so the binary and a
// host share one source of truth.
package osrm

import (
	"context"
	"fmt"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

func init() { kit.Register(Domain{}) }

// Domain is the OSRM driver. It carries no state; the per-run client is
// built by the factory Register hands to kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against,
// and the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "osrm",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "osrm",
			Short:  "OSRM road routing (router.project-osrm.org)",
			Long: `osrm queries the OSRM (Open Source Routing Machine) demo server for
road routing, nearest road segments, and distance/duration matrices.
No API key required. Coordinates are always given as lat,lon.`,
			Site: Host,
			Repo: "https://github.com/tamnd/osrm-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	// route: find driving/cycling/walking route between 2+ waypoints
	kit.Handle(app, kit.OpMeta{
		Name:    "route",
		Group:   "read",
		List:    true,
		Summary: "Find driving route between two or more waypoints (lat,lon)",
		Args: []kit.Arg{
			{Name: "from", Help: "origin coordinate as lat,lon"},
			{Name: "to", Help: "destination coordinate as lat,lon"},
		},
	}, routeOp)

	// nearest: find nearest road segment to a coordinate
	kit.Handle(app, kit.OpMeta{
		Name:    "nearest",
		Group:   "read",
		List:    true,
		Summary: "Find nearest road segment to a coordinate (lat,lon)",
		Args:    []kit.Arg{{Name: "location", Help: "coordinate as lat,lon"}},
	}, nearestOp)

	// table: compute distance/duration matrix between multiple coordinates
	kit.Handle(app, kit.OpMeta{
		Name:    "table",
		Group:   "read",
		List:    true,
		Summary: "Compute distance/duration matrix between coordinates (lat,lon ...)",
		Args:    []kit.Arg{{Name: "coords", Help: "two or more coordinates as lat,lon", Variadic: true}},
	}, tableOp)
}

// newClient builds the client from the host-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := DefaultConfig()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.Timeout = cfg.Timeout
	}
	return NewClient(c), nil
}

// --- inputs ---

type routeInput struct {
	From   string  `kit:"arg"  help:"origin coordinate as lat,lon"`
	To     string  `kit:"arg"  help:"destination coordinate as lat,lon"`
	Mode   string  `kit:"flag" help:"transport mode: driving, cycling, walking (default driving)"`
	Client *Client `kit:"inject"`
}

type nearestInput struct {
	Location string  `kit:"arg"  help:"coordinate as lat,lon"`
	Count    int     `kit:"flag" help:"number of nearest points to return (default 1)"`
	Mode     string  `kit:"flag" help:"transport mode: driving, cycling, walking (default driving)"`
	Client   *Client `kit:"inject"`
}

type tableInput struct {
	Coords []string `kit:"arg,variadic" help:"two or more coordinates as lat,lon"`
	Mode   string   `kit:"flag"         help:"transport mode: driving, cycling, walking (default driving)"`
	Client *Client  `kit:"inject"`
}

// --- handlers ---

func routeOp(ctx context.Context, in routeInput, emit func(Route) error) error {
	mode := in.Mode
	if mode == "" {
		mode = "driving"
	}
	routes, err := in.Client.Route(ctx, []string{in.From, in.To}, mode)
	if err != nil {
		return mapErr(err)
	}
	for _, r := range routes {
		if err := emit(r); err != nil {
			return err
		}
	}
	return nil
}

func nearestOp(ctx context.Context, in nearestInput, emit func(NearestPoint) error) error {
	mode := in.Mode
	if mode == "" {
		mode = "driving"
	}
	count := in.Count
	if count <= 0 {
		count = 1
	}
	pts, err := in.Client.Nearest(ctx, in.Location, count, mode)
	if err != nil {
		return mapErr(err)
	}
	for _, p := range pts {
		if err := emit(p); err != nil {
			return err
		}
	}
	return nil
}

func tableOp(ctx context.Context, in tableInput, emit func(MatrixRow) error) error {
	if len(in.Coords) < 2 {
		return errs.Usage("table requires at least 2 coordinates")
	}
	mode := in.Mode
	if mode == "" {
		mode = "driving"
	}
	rows, err := in.Client.Table(ctx, in.Coords, mode)
	if err != nil {
		return mapErr(err)
	}
	for _, row := range rows {
		if err := emit(row); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: pure string functions, no network ---

// Classify turns an input into the canonical (type, id).
func (Domain) Classify(input string) (uriType, id string, err error) {
	if input == "" {
		return "", "", errs.Usage("empty osrm reference")
	}
	return "route", input, nil
}

// Locate returns the live http URL for a (type, id).
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "route":
		return fmt.Sprintf("%s/route/v1/driving/%s?overview=false", BaseURL, id), nil
	default:
		return "", errs.Usage("osrm has no resource type %q", uriType)
	}
}

// mapErr converts a library error into the kit error kind.
func mapErr(err error) error {
	return err
}
