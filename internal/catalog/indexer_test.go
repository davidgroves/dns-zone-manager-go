package catalog

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

type fakeTransfer struct {
	mu      sync.Mutex
	zone    *dnsx.Zone
	serial  uint32
	axfrErr error
	soaErr  error
	axfrs   int
}

func (f *fakeTransfer) PerformAXFR(_ context.Context, zone string) (*dnsx.Zone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.axfrs++
	if f.axfrErr != nil {
		return nil, f.axfrErr
	}
	if f.zone == nil {
		return nil, &dnsx.ZoneTransferError{Message: "no zone"}
	}
	out := dnsx.NewZone(zone)
	_ = out.SetFromAXFR(f.zone.AllRRs())
	return out, nil
}

func (f *fakeTransfer) PerformIXFR(context.Context, string, uint32) (dnsx.IXFRResult, error) {
	return dnsx.IXFRResult{}, fmt.Errorf("ixfr not supported")
}

func (f *fakeTransfer) QuerySOA(_ context.Context, _ string) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.soaErr != nil {
		return 0, f.soaErr
	}
	return f.serial, nil
}

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		t.Fatal(err)
	}
	return rr
}

func makeCatalogZone(t *testing.T, origin string, serial uint32, soaRefresh uint32, members map[string]string) *dnsx.Zone {
	t.Helper()
	origin = dnsx.NormalizeZoneName(origin)
	if soaRefresh == 0 {
		soaRefresh = 3600
	}
	rrs := []dns.RR{
		mustRR(t, fmt.Sprintf("%s 3600 IN SOA ns.%s host.%s %d %d 60 3600 60", origin, origin, origin, serial, soaRefresh)),
		mustRR(t, fmt.Sprintf("%s 3600 IN NS invalid.", origin)),
		mustRR(t, fmt.Sprintf("version.%s 3600 IN TXT \"2\"", origin)),
	}
	for id, member := range members {
		rrs = append(rrs, mustRR(t, fmt.Sprintf("%s.zones.%s 3600 IN PTR %s", id, origin, dnsx.NormalizeZoneName(member))))
	}
	// Noise: PTR under a property tree should be ignored.
	rrs = append(rrs, mustRR(t, fmt.Sprintf("extra.prop.zones.%s 3600 IN PTR noise.example.", origin)))

	z := dnsx.NewZone(origin)
	if err := z.SetFromAXFR(rrs); err != nil {
		t.Fatal(err)
	}
	return z
}

func TestExtractMemberZones(t *testing.T) {
	z := makeCatalogZone(t, "catalog.example.", 1, 3600, map[string]string{
		"example-com": "example.com.",
		"test-local":  "test.local",
	})
	got := ExtractMemberZones(z)
	if len(got) != 2 {
		t.Fatalf("members=%v", got)
	}
	if got[0] != "example.com." || got[1] != "test.local." {
		t.Fatalf("unexpected members: %v", got)
	}
	ver, ok := CatalogVersionTXT(z)
	if !ok || ver != "2" {
		t.Fatalf("version=%q ok=%v", ver, ok)
	}
}

func TestIndexerRefreshAndListZones(t *testing.T) {
	origin := "catalog.example."
	z := makeCatalogZone(t, origin, 10, 120, map[string]string{
		"a": "alpha.test.",
		"b": "beta.test.",
	})
	backend := &fakeTransfer{zone: z, serial: 10}
	idx := New(Config{
		Enabled:      true,
		ZoneName:     origin,
		PollInterval: time.Hour,
	}, backend)

	if err := idx.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	zones, err := idx.ListZones()
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 2 {
		t.Fatalf("zones=%v", zones)
	}
	st := idx.Status()
	if !st.Connected || st.ZonesDiscovered != 2 || st.Serial == nil || *st.Serial != 10 {
		t.Fatalf("status=%+v", st)
	}
	// SOA refresh (120s) is lower than configured hour → effective poll shrinks.
	if st.PollInterval != 120 {
		t.Fatalf("poll_interval=%v want 120", st.PollInterval)
	}
}

func TestIndexerListZonesBeforeLoad(t *testing.T) {
	idx := New(Config{ZoneName: "catalog.example."}, &fakeTransfer{})
	if _, err := idx.ListZones(); err == nil {
		t.Fatal("expected error before load")
	}
}

func TestIndexerStartStopPollRefresh(t *testing.T) {
	origin := "catalog.example."
	// Keep SOA refresh high so configured poll interval wins.
	z := makeCatalogZone(t, origin, 1, 3600, map[string]string{"m": "member.example."})
	backend := &fakeTransfer{zone: z, serial: 1}
	idx := New(Config{
		Enabled:      true,
		ZoneName:     origin,
		PollInterval: 30 * time.Millisecond,
		// Leave notify ports at 0 so we don't bind in tests.
		NotifyUDPPort: 0,
		NotifyTCPPort: 0,
	}, backend)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := idx.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer idx.Stop()

	zones, err := idx.ListZones()
	if err != nil || len(zones) != 1 || zones[0] != "member.example." {
		t.Fatalf("zones=%v err=%v", zones, err)
	}

	// Bump serial and zone contents; poll should AXFR again.
	z2 := makeCatalogZone(t, origin, 2, 3600, map[string]string{
		"m": "member.example.",
		"n": "new.example.",
	})
	backend.mu.Lock()
	backend.zone = z2
	backend.serial = 2
	backend.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		zones, err = idx.ListZones()
		if err == nil && len(zones) == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not observe poll refresh; zones=%v axfrs=%d", zones, backend.axfrs)
}

func TestIndexerStartRequiresZoneName(t *testing.T) {
	idx := New(Config{}, &fakeTransfer{})
	if err := idx.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
