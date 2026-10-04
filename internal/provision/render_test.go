package provision

import (
	"strings"
	"testing"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/miekg/dns"
)

func TestRenderSeedZoneDefaultsAndOverrides(t *testing.T) {
	d := config.RNDCZoneDefaults{
		PrimaryNS:   "ns1.example.",
		AdminEmail:  "hostmaster@example.com",
		Nameservers: []string{"ns1.example.", "ns2.example."},
		TTL:         3600,
		Refresh:     3600,
		Retry:       600,
		Expire:      86400,
		Minimum:     60,
	}
	ttl := uint32(300)
	text := renderSeedZone("New.Example", d, CreateRequest{
		PrimaryNS: "ns.other.",
		TTL:       &ttl,
		SOA:       &SOAParams{Refresh: 100},
	}, 42)
	if !strings.Contains(text, "$ORIGIN new.example.") {
		t.Fatalf("origin: %s", text)
	}
	if !strings.Contains(text, "$TTL 300") {
		t.Fatalf("ttl override: %s", text)
	}
	if !strings.Contains(text, "SOA ns.other. hostmaster.example.") {
		t.Fatalf("soa: %s", text)
	}
	if !strings.Contains(text, "42 100 600 86400 60") {
		t.Fatalf("timers: %s", text)
	}
	if !strings.Contains(text, "IN NS ns1.example.") || !strings.Contains(text, "IN NS ns2.example.") {
		t.Fatalf("ns: %s", text)
	}
}

func TestRenderAddzoneConfigSharedDir(t *testing.T) {
	r := config.RNDCSettings{
		ZoneSeed: config.RNDCZoneSeed{
			Mode:    config.RNDCSeedSharedDir,
			BindDir: "/managed-zones",
		},
		ZoneTemplate: config.RNDCZoneTemplate{
			AllowUpdateKey:   "dns-api-key",
			AllowTransferKey: "dns-api-key",
		},
	}
	got := renderAddzoneConfig("foo.example.", r)
	want := `{ type primary; file "/managed-zones/db.foo.example"; allow-update { key "dns-api-key"; }; allow-transfer { key "dns-api-key"; }; }`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderAddzoneConfigInitialFile(t *testing.T) {
	r := config.RNDCSettings{
		ZoneSeed: config.RNDCZoneSeed{
			Mode:        config.RNDCSeedInitialFile,
			BindDir:     "/var/cache/bind",
			InitialFile: "/etc/bind/empty.zone",
		},
		ZoneTemplate: config.RNDCZoneTemplate{AllowUpdateKey: "k", AllowTransferKey: "k"},
	}
	got := renderAddzoneConfig("bar.test.", r)
	if !strings.Contains(got, `file "/var/cache/bind/db.bar.test"`) {
		t.Fatalf("file: %s", got)
	}
	if !strings.Contains(got, `initial-file "/etc/bind/empty.zone"`) {
		t.Fatalf("initial-file: %s", got)
	}
}

func TestMemberLabelStable(t *testing.T) {
	a := memberLabel("Example.COM.")
	b := memberLabel("example.com")
	if a != b || len(a) != 40 {
		t.Fatalf("label %q / %q", a, b)
	}
}

func TestCatalogPTROwners(t *testing.T) {
	z := dnsx.NewZone("catalog.example.")
	rrs := []dns.RR{
		mustRR(t, "catalog.example. 3600 IN SOA ns.catalog.example. host.catalog.example. 1 3600 600 86400 60"),
		mustRR(t, "example-com.zones.catalog.example. 3600 IN PTR example.com."),
		mustRR(t, "other.zones.catalog.example. 3600 IN PTR other.test."),
	}
	if err := z.SetFromAXFR(rrs); err != nil {
		t.Fatal(err)
	}
	owners := catalogPTROwners(z, "catalog.example.", "example.com.")
	if len(owners) != 1 || owners[0] != "example-com.zones.catalog.example." {
		t.Fatalf("owners=%v", owners)
	}
}

func TestTypedErrors(t *testing.T) {
	if ErrDisabled == nil || ErrZoneExists == nil || ErrZoneNotFound == nil || ErrNotReady == nil {
		t.Fatal("typed errors missing")
	}
}

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		t.Fatal(err)
	}
	return rr
}
