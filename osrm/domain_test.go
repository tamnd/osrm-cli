package osrm

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// TestDomainInfo checks basic domain metadata.
func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "osrm" {
		t.Errorf("Scheme = %q, want osrm", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "osrm" {
		t.Errorf("Identity.Binary = %q, want osrm", info.Identity.Binary)
	}
}

// TestClassify checks the URI classifier.
func TestClassify(t *testing.T) {
	cases := []struct {
		in, typ, id string
	}{
		{"51.5074,-0.1276", "route", "51.5074,-0.1276"},
		{"48.8566,2.3522", "route", "48.8566,2.3522"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil || typ != tc.typ || id != tc.id {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.typ, tc.id)
		}
	}
}

// TestClassifyEmpty checks that an empty string returns an error.
func TestClassifyEmpty(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Error("Classify(\"\") should return an error")
	}
}

// TestLocate checks that Locate builds the right URL.
func TestLocate(t *testing.T) {
	got, err := Domain{}.Locate("route", "-0.1276,51.5074;2.3522,48.8566")
	if err != nil {
		t.Fatalf("Locate error: %v", err)
	}
	if got == "" {
		t.Error("Locate returned empty URL")
	}
}

// TestLocateUnknownType checks that an unknown type returns an error.
func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("page", "foo")
	if err == nil {
		t.Error("Locate with unknown type should return an error")
	}
}

// TestHostWiring mounts the driver in a kit Host and checks it registers.
func TestHostWiring(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the osrm domain is registered.
	domains := h.Domains()
	found := false
	for _, d := range domains {
		if d == "osrm" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("osrm domain not registered; domains = %v", domains)
	}

	// ResolveOn should build an osrm:// URI for a route coordinate string.
	got, err := h.ResolveOn("osrm", "51.5074,-0.1276")
	if err != nil {
		t.Fatalf("ResolveOn: %v", err)
	}
	if got.Scheme != "osrm" {
		t.Errorf("ResolveOn scheme = %q, want osrm", got.Scheme)
	}
}
