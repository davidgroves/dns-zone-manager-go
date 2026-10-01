package dnsx

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

type fakeBackend struct {
	mu      sync.Mutex
	zones   map[string]*Zone
	serials map[string]uint32
	ixfr    map[string]IXFRResult
	axfrErr error
	soaErr  error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		zones:   make(map[string]*Zone),
		serials: make(map[string]uint32),
		ixfr:    make(map[string]IXFRResult),
	}
}

func (f *fakeBackend) PerformAXFR(_ context.Context, zone string) (*Zone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.axfrErr != nil {
		return nil, f.axfrErr
	}
	z, ok := f.zones[NormalizeZoneName(zone)]
	if !ok {
		return nil, &ZoneTransferError{Message: "not found"}
	}
	// Return a copy-like zone rebuilt from RRs.
	out := NewZone(zone)
	_ = out.SetFromAXFR(z.AllRRs())
	return out, nil
}

func (f *fakeBackend) PerformIXFR(_ context.Context, zone string, fromSerial uint32) (IXFRResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res, ok := f.ixfr[NormalizeZoneName(zone)]
	if !ok {
		return IXFRResult{}, &ZoneTransferError{Message: "no ixfr"}
	}
	if fromSerial != 0 && !Less(fromSerial, res.NewSerial) && fromSerial != res.NewSerial {
		return IXFRResult{}, &ZoneTransferError{Message: "not eligible"}
	}
	return res, nil
}

func (f *fakeBackend) QuerySOA(_ context.Context, zone string) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.soaErr != nil {
		return 0, f.soaErr
	}
	s, ok := f.serials[NormalizeZoneName(zone)]
	if !ok {
		return 0, fmt.Errorf("no soa")
	}
	return s, nil
}

func testSettings() *config.Settings {
	return &config.Settings{
		Cache: config.CacheSettings{
			Enabled:               true,
			MinRefreshInterval:    1,
			MaxRefreshInterval:    3600,
			MaxSizeBytes:          10 << 20,
			MaxZoneSizeBytes:      10 << 20,
			SerialRefreshDebounce: 20 * time.Millisecond,
		},
		Notify: config.NotifySettings{
			PreferIXFR: true,
		},
	}
}

func makeTestZone(t *testing.T, origin string, serial uint32) *Zone {
	t.Helper()
	z := NewZone(origin)
	soa := mustRR(t, fmt.Sprintf("%s 3600 IN SOA ns.%s host.%s %d 7200 3600 1209600 3600", origin, origin, origin, serial))
	a := mustRR(t, fmt.Sprintf("www.%s 300 IN A 192.0.2.1", origin))
	if err := z.SetFromAXFR([]dns.RR{soa, a}); err != nil {
		t.Fatal(err)
	}
	return z
}

func TestCacheLoadAndList(t *testing.T) {
	fb := newFakeBackend()
	origin := "example.com."
	fb.zones[origin] = makeTestZone(t, origin, 1)
	fb.serials[origin] = 1

	cache := NewCacheWithBackend(testSettings(), fb)
	cz, err := cache.LoadZone(context.Background(), origin)
	if err != nil {
		t.Fatal(err)
	}
	if cz.Serial != 1 {
		t.Fatalf("serial=%d", cz.Serial)
	}
	zones := cache.ListZones()
	if len(zones) != 1 || zones[0] != origin {
		t.Fatalf("zones=%v", zones)
	}
	if cache.PeekZone(origin) == nil {
		t.Fatal("peek missed")
	}
}

func TestCacheIXFRRefresh(t *testing.T) {
	fb := newFakeBackend()
	origin := "example.com."
	fb.zones[origin] = makeTestZone(t, origin, 100)
	fb.serials[origin] = 200

	del := mustRR(t, "www.example.com. 300 IN A 192.0.2.1")
	add := mustRR(t, "www.example.com. 300 IN A 192.0.2.2")
	fb.ixfr[origin] = IXFRResult{
		IsFullAXFR: false,
		Deletes:    []dns.RR{del},
		Adds:       []dns.RR{add},
		NewSerial:  200,
		Operations: []Operation{
			{Action: "delete", Name: "www.example.com.", Type: "A", Class: "IN", Records: []string{"192.0.2.1"}},
			{Action: "add", Name: "www.example.com.", Type: "A", Class: "IN", TTL: 300, Records: []string{"192.0.2.2"}},
		},
	}

	cache := NewCacheWithBackend(testSettings(), fb)
	if _, err := cache.LoadZone(context.Background(), origin); err != nil {
		t.Fatal(err)
	}

	cz, ops, err := cache.RefreshZoneWithOps(context.Background(), origin, false)
	if err != nil {
		t.Fatal(err)
	}
	if ops == nil {
		t.Fatal("expected ops from IXFR")
	}
	if cz.Serial != 200 {
		t.Fatalf("serial=%d", cz.Serial)
	}
	info, ok := cz.Zone.GetRRset("www", dns.TypeA)
	if !ok || len(info.Records) != 1 || info.Records[0] != "192.0.2.2" {
		t.Fatalf("rrset=%+v ok=%v", info, ok)
	}
}

func TestCacheSerialUnchanged(t *testing.T) {
	fb := newFakeBackend()
	origin := "example.com."
	fb.zones[origin] = makeTestZone(t, origin, 5)
	fb.serials[origin] = 5

	cache := NewCacheWithBackend(testSettings(), fb)
	if _, err := cache.LoadZone(context.Background(), origin); err != nil {
		t.Fatal(err)
	}
	_, ops, err := cache.RefreshZoneWithOps(context.Background(), origin, false)
	if err != nil {
		t.Fatal(err)
	}
	if ops == nil || len(ops) != 0 {
		t.Fatalf("expected empty ops, got %#v", ops)
	}
}

func TestCacheSyncFromCatalog(t *testing.T) {
	fb := newFakeBackend()
	a := "a.example."
	b := "b.example."
	fb.zones[a] = makeTestZone(t, a, 1)
	fb.zones[b] = makeTestZone(t, b, 1)
	fb.serials[a] = 1
	fb.serials[b] = 1

	cache := NewCacheWithBackend(testSettings(), fb)
	res := cache.SyncFromCatalog(context.Background(), []string{a, b}, false)
	if res[a] != "added" || res[b] != "added" {
		t.Fatalf("res=%v", res)
	}
	res2 := cache.SyncFromCatalog(context.Background(), []string{a}, true)
	if res2[a] != "exists" || res2[b] != "removed" {
		t.Fatalf("res2=%v", res2)
	}
	if cache.PeekZone(b) != nil {
		t.Fatal("stale zone still present")
	}
}

func TestCacheEviction(t *testing.T) {
	fb := newFakeBackend()
	settings := testSettings()
	settings.Cache.MaxSizeBytes = 2500
	settings.Cache.MaxZoneSizeBytes = 2500

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("z%d.example.", i)
		fb.zones[name] = makeTestZone(t, name, 1)
		fb.serials[name] = 1
	}

	cache := NewCacheWithBackend(settings, fb)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("z%d.example.", i)
		if _, err := cache.LoadZone(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	if cache.Len() >= 5 {
		t.Fatalf("expected eviction, len=%d size=%d", cache.Len(), cache.SizeBytes())
	}
}

func TestCacheDebouncedSerialRefresh(t *testing.T) {
	fb := newFakeBackend()
	origin := "example.com."
	fb.zones[origin] = makeTestZone(t, origin, 1)
	fb.serials[origin] = 1

	cache := NewCacheWithBackend(testSettings(), fb)
	if _, err := cache.LoadZone(context.Background(), origin); err != nil {
		t.Fatal(err)
	}

	fb.mu.Lock()
	fb.serials[origin] = 99
	fb.mu.Unlock()

	cache.UpdateCacheAfterAdd(origin, "www", 300, "A", "IN", []string{"192.0.2.9"})
	cache.UpdateCacheAfterAdd(origin, "www", 300, "A", "IN", []string{"192.0.2.10"})

	deadline := time.Now().Add(500 * time.Millisecond)
	var serial uint32
	for time.Now().Before(deadline) {
		cz := cache.PeekZone(origin)
		if cz != nil {
			cz.mu.RLock()
			serial = cz.Serial
			cz.mu.RUnlock()
			if serial == 99 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("serial not refreshed, got %d", serial)
}

func TestPrepareAddPrereqs(t *testing.T) {
	settings := &config.Settings{
		DNS: config.DNSSettings{
			Server:        "127.0.0.1",
			Port:          53,
			UpdateTSIGKey: "test-key",
			Timeout:       time.Second,
			AXFRTimeout:   time.Second,
			PoolSize:      1,
		},
		TSIGKeys: []config.TSIGKeyEntry{
			{Name: "test-key", Algorithm: "hmac-sha256"},
		},
	}
	settings.TSIGKeys[0].Secret.Set("dGVzdA==") // "test" base64

	c, err := NewClient(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	msg, err := c.PrepareAdd("example.com.", "www", 300, "A", "IN", []string{"192.0.2.1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Answer) < 2 {
		t.Fatalf("expected NXRRSET prereqs for type+CNAME, got %d", len(msg.Answer))
	}

	cnameMsg, err := c.PrepareAdd("example.com.", "alias", 300, "CNAME", "IN", []string{"www.example.com."}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cnameMsg.Answer) != 1 {
		t.Fatalf("CNAME should use single NXDOMAIN prereq, got %d", len(cnameMsg.Answer))
	}
}
