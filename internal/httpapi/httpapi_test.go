package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/httpapi"
	"github.com/davidgroves/dns-zone-manager-go/internal/provision"
)

func testSettings() *config.Settings {
	return &config.Settings{
		AppName: "DNS Zone Manager Test",
		Server:  config.ServerSettings{MaxBodyBytes: 1 << 20},
		Logging: config.LoggingSettings{
			Format: "json", Level: "error", SampleRate: 0, SlowThresholdMS: 1000,
		},
		Cache:     config.CacheSettings{Enabled: true},
		DNS:       config.DNSSettings{Server: "127.0.0.1", Port: 53},
		APIKey:    config.APIKeySettings{Enabled: false},
		ProxyAuth: config.ProxyAuthSettings{Enabled: false},
		Theme:     config.ThemeSettings{DefaultMode: "light", AllowModeToggle: true},
		NSUpdate: config.NSUpdateSettings{
			MaxBodyBytes: 1 << 20, MaxLines: 1000, MaxTransactions: 100,
		},
	}
}

type fakeBackend struct {
	zones map[string]*dnsx.Zone
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{zones: map[string]*dnsx.Zone{}}
}

func parseZoneRRs(origin string, serial uint32) []dns.RR {
	origin = dnsx.NormalizeZoneName(origin)
	text := fmt.Sprintf("%s 3600 IN SOA ns.%s hostmaster.%s %d 7200 3600 1209600 3600\n", origin, origin, origin, serial)
	text += fmt.Sprintf("%s 3600 IN NS ns.%s\n", origin, origin)
	text += fmt.Sprintf("www.%s 300 IN A 192.0.2.1\n", origin)
	text += fmt.Sprintf("api.%s 300 IN A 192.0.2.2\n", origin)
	var out []dns.RR
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		rr, err := dns.NewRR(line)
		if err != nil {
			panic(err)
		}
		out = append(out, rr)
	}
	return out
}

func (f *fakeBackend) PerformAXFR(_ context.Context, zone string) (*dnsx.Zone, error) {
	zone = dnsx.NormalizeZoneName(zone)
	z, ok := f.zones[zone]
	if !ok {
		return nil, &dnsx.ZoneTransferError{Message: "zone not found: " + zone}
	}
	return z, nil
}

func (f *fakeBackend) PerformIXFR(_ context.Context, zone string, fromSerial uint32) (dnsx.IXFRResult, error) {
	zone = dnsx.NormalizeZoneName(zone)
	if _, ok := f.zones[zone]; !ok {
		return dnsx.IXFRResult{}, &dnsx.ZoneTransferError{Message: "zone not found"}
	}
	return dnsx.IXFRResult{NewSerial: fromSerial}, nil
}

func (f *fakeBackend) QuerySOA(_ context.Context, zone string) (uint32, error) {
	zone = dnsx.NormalizeZoneName(zone)
	z, ok := f.zones[zone]
	if !ok {
		return 0, &dnsx.ZoneTransferError{Message: "zone not found"}
	}
	s, ok := z.SOASerial()
	if !ok {
		return 1, nil
	}
	return s, nil
}

func (f *fakeBackend) seed(zone string, serial uint32) {
	zone = dnsx.NormalizeZoneName(zone)
	z := dnsx.NewZone(zone)
	if err := z.SetFromAXFR(parseZoneRRs(zone, serial)); err != nil {
		panic(err)
	}
	f.zones[zone] = z
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	settings := testSettings()
	backend := newFakeBackend()
	backend.seed("example.com.", 1)
	backend.seed("other.com.", 1)
	cache := dnsx.NewCacheWithBackend(settings, backend)
	if _, err := cache.LoadZone(context.Background(), "example.com."); err != nil {
		t.Fatalf("load zone: %v", err)
	}
	if _, err := cache.LoadZone(context.Background(), "other.com."); err != nil {
		t.Fatalf("load zone: %v", err)
	}
	return httpapi.New(httpapi.Deps{
		Settings:   settings,
		Auth:       auth.NewCombined(settings),
		Cache:      cache,
		DNSReady:   func(context.Context) bool { return true },
		StoreReady: func(context.Context) bool { return true },
	})
}

func doJSON(t *testing.T, h http.Handler, method, path string) (int, map[string]any, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var body map[string]any
	b, _ := io.ReadAll(rr.Body)
	if len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	return rr.Code, body, rr.Header()
}

func TestUIDirServesIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>from-dir</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	h := httpapi.New(httpapi.Deps{
		Settings:   settings,
		Auth:       auth.NewCombined(settings),
		UIDir:      dir,
		DNSReady:   func(context.Context) bool { return true },
		StoreReady: func(context.Context) bool { return true },
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "from-dir") {
		t.Fatalf("body %q", rr.Body.String())
	}
}

func TestRootBrowserGetsSPA(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "<html") {
		t.Fatalf("body %q", rr.Body.String())
	}
}

func TestRootClientGetsAPIInfo(t *testing.T) {
	h := testHandler(t)
	code, body, _ := doJSON(t, h, http.MethodGet, "/")
	if code != http.StatusOK {
		t.Fatalf("status %d body=%v", code, body)
	}
	if body["health"] != "/health" {
		t.Fatalf("body %v", body)
	}
}

func TestHealth(t *testing.T) {
	h := testHandler(t)
	code, body, hdr := doJSON(t, h, http.MethodGet, "/health")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if body["status"] != "healthy" {
		t.Fatalf("status=%v", body["status"])
	}
	if body["dns_connected"] != true {
		t.Fatalf("dns_connected=%v", body["dns_connected"])
	}
	_ = hdr.Get("X-Request-ID") // /health is excluded from WideEvent — request id optional
}

func TestUIConfig(t *testing.T) {
	h := testHandler(t)
	code, body, _ := doJSON(t, h, http.MethodGet, "/ui/config")
	if code != http.StatusOK {
		t.Fatalf("status %d body=%v", code, body)
	}
	if body["apiKeyEnabled"] != false {
		t.Fatalf("apiKeyEnabled=%v", body["apiKeyEnabled"])
	}
	if body["version"] == nil {
		t.Fatal("missing version")
	}
	theme, ok := body["theme"].(map[string]any)
	if !ok || theme["appName"] == nil {
		t.Fatalf("theme=%v", body["theme"])
	}
}

func TestAuthValidate(t *testing.T) {
	h := testHandler(t)
	code, body, _ := doJSON(t, h, http.MethodGet, "/v1/auth/validate")
	if code != http.StatusOK {
		t.Fatalf("status %d body=%v", code, body)
	}
	if body["valid"] != true {
		t.Fatalf("valid=%v", body["valid"])
	}
	if body["user_id"] != "anonymous" {
		t.Fatalf("user_id=%v", body["user_id"])
	}
	if body["auth_type"] != "none" {
		t.Fatalf("auth_type=%v", body["auth_type"])
	}
}

func TestListZonesPaginationCursorRoundtrip(t *testing.T) {
	h := testHandler(t)
	limit := 1
	code, body, hdr := doJSON(t, h, http.MethodGet, fmt.Sprintf("/v1/zones?limit=%d", limit))
	if code != http.StatusOK {
		t.Fatalf("status %d body=%v", code, body)
	}
	if hdr.Get("X-Request-ID") == "" {
		t.Fatal("expected X-Request-ID on response")
	}
	zones, _ := body["zones"].([]any)
	if len(zones) != 1 {
		t.Fatalf("expected 1 zone, got %d (%v)", len(zones), body)
	}
	total, _ := body["total_count"].(float64)
	if total < 2 {
		t.Fatalf("total_count=%v", body["total_count"])
	}
	if body["has_more"] != true {
		t.Fatalf("has_more=%v", body["has_more"])
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("expected next_cursor")
	}
	// Round-trip Decode
	var payload httpapi.ZoneCursor
	if err := httpapi.DecodeCursor(cursor, &payload); err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if payload.N == "" {
		t.Fatal("cursor missing n")
	}
	enc, err := httpapi.EncodeCursor(payload)
	if err != nil || enc != cursor {
		t.Fatalf("encode roundtrip got %q want %q err=%v", enc, cursor, err)
	}

	code2, body2, _ := doJSON(t, h, http.MethodGet, "/v1/zones?limit=1&after="+cursor)
	if code2 != http.StatusOK {
		t.Fatalf("page2 status %d", code2)
	}
	zones2, _ := body2["zones"].([]any)
	if len(zones2) != 1 {
		t.Fatalf("page2 zones=%v", body2)
	}
	z1 := zones[0].(map[string]any)["zone"]
	z2 := zones2[0].(map[string]any)["zone"]
	if z1 == z2 {
		t.Fatalf("expected different zones across pages, both %v", z1)
	}
}

func TestProblemJSONNotFound(t *testing.T) {
	h := testHandler(t)
	code, body, hdr := doJSON(t, h, http.MethodGet, "/v1/zones/missing.example.")
	if code != http.StatusNotFound {
		t.Fatalf("status %d body=%v", code, body)
	}
	ct := hdr.Get("Content-Type")
	if !strings.Contains(ct, "problem+json") && !strings.Contains(ct, "json") {
		t.Fatalf("content-type=%q", ct)
	}
	if body["status"] != float64(404) && body["title"] == nil && body["detail"] == nil {
		t.Fatalf("expected problem+json shape, got %v", body)
	}
	// Huma ErrorModel shape
	if body["detail"] == nil && body["title"] == nil {
		t.Fatalf("missing detail/title: %v", body)
	}
}

func TestSessionCookieAuth(t *testing.T) {
	settings := testSettings()
	var secret config.Secret
	secret.Set("k1")
	settings.APIKey = config.APIKeySettings{
		Enabled:    true,
		HeaderName: "X-API-Key",
		Keys:       map[string]config.Secret{"ops": secret},
	}
	backend := newFakeBackend()
	backend.seed("example.com.", 1)
	cache := dnsx.NewCacheWithBackend(settings, backend)
	if _, err := cache.LoadZone(context.Background(), "example.com."); err != nil {
		t.Fatalf("load zone: %v", err)
	}
	h := httpapi.New(httpapi.Deps{
		Settings:   settings,
		Auth:       auth.NewCombined(settings),
		Cache:      cache,
		DNSReady:   func(context.Context) bool { return true },
		StoreReady: func(context.Context) bool { return true },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", strings.NewReader(`{"remember":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "k1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("session status %d body=%s", rr.Code, rr.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("missing session cookie")
	}
	if !cookie.HttpOnly {
		t.Fatal("cookie must be HttpOnly")
	}

	val := httptest.NewRequest(http.MethodGet, "/v1/auth/validate", nil)
	val.AddCookie(cookie)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, val)
	if rr2.Code != http.StatusOK {
		t.Fatalf("validate status %d body=%s", rr2.Code, rr2.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["user_id"] != "ops" {
		t.Fatalf("body=%v", body)
	}

	out := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	out.AddCookie(cookie)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, out)
	if rr3.Code != http.StatusOK {
		t.Fatalf("logout %d", rr3.Code)
	}
	val2 := httptest.NewRequest(http.MethodGet, "/v1/auth/validate", nil)
	val2.AddCookie(cookie)
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, val2)
	if rr4.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d %s", rr4.Code, rr4.Body.String())
	}
}

func TestCursorEncodeDecodeVariants(t *testing.T) {
	rr, err := httpapi.EncodeCursor(httpapi.RRsetCursorPayload{N: "www.example.com.", T: "A"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded httpapi.RRsetCursorPayload
	if err := httpapi.DecodeCursor(rr, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.N != "www.example.com." || decoded.T != "A" {
		t.Fatalf("%+v", decoded)
	}

	g, err := httpapi.EncodeCursor(httpapi.GlobalSearchCursor{Z: "example.com.", N: "www.example.com."})
	if err != nil {
		t.Fatal(err)
	}
	var gd httpapi.GlobalSearchCursor
	if err := httpapi.DecodeCursor(g, &gd); err != nil {
		t.Fatal(err)
	}
	if gd.Z != "example.com." || gd.N != "www.example.com." {
		t.Fatalf("%+v", gd)
	}
}

type fakeProvisioner struct {
	status         provision.Status
	create         provision.Result
	createErr      error
	delete         provision.Result
	deleteErr      error
	catalogAdd     provision.Result
	catalogAddErr  error
	catalogRem     provision.Result
	catalogRemErr  error
	created        []provision.CreateRequest
	deleted        []string
	catalogAdded   []string
	catalogRemoved []string
}

func (f *fakeProvisioner) CreateZone(_ context.Context, req provision.CreateRequest) (provision.Result, error) {
	f.created = append(f.created, req)
	if f.createErr != nil {
		return provision.Result{}, f.createErr
	}
	res := f.create
	if res.Zone == "" {
		res.Zone = req.Zone
	}
	return res, nil
}

func (f *fakeProvisioner) DeleteZone(_ context.Context, zone string, _ provision.DeleteOptions) (provision.Result, error) {
	f.deleted = append(f.deleted, zone)
	if f.deleteErr != nil {
		return provision.Result{}, f.deleteErr
	}
	res := f.delete
	if res.Zone == "" {
		res.Zone = zone
	}
	return res, nil
}

func (f *fakeProvisioner) AddToCatalog(_ context.Context, zone string) (provision.Result, error) {
	f.catalogAdded = append(f.catalogAdded, zone)
	if f.catalogAddErr != nil {
		return provision.Result{}, f.catalogAddErr
	}
	res := f.catalogAdd
	if res.Zone == "" {
		res.Zone = zone
	}
	if !res.CatalogAdded {
		res.CatalogAdded = true
	}
	return res, nil
}

func (f *fakeProvisioner) RemoveFromCatalog(_ context.Context, zone string) (provision.Result, error) {
	f.catalogRemoved = append(f.catalogRemoved, zone)
	if f.catalogRemErr != nil {
		return provision.Result{}, f.catalogRemErr
	}
	res := f.catalogRem
	if res.Zone == "" {
		res.Zone = zone
	}
	if !res.CatalogRemoved {
		res.CatalogRemoved = true
	}
	return res, nil
}

func (f *fakeProvisioner) Status(context.Context) provision.Status { return f.status }

func TestRNDCStatusDisabled(t *testing.T) {
	h := testHandler(t)
	code, body, _ := doJSON(t, h, http.MethodGet, "/v1/rndc/status")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if body["enabled"] != false {
		t.Fatalf("body=%v", body)
	}
}

func TestCreateZoneRequiresRNDC(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/zones", strings.NewReader(`{"zone":"new.example."}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAndDeleteZoneWithProvisioner(t *testing.T) {
	settings := testSettings()
	serial := uint32(1)
	fp := &fakeProvisioner{
		status: provision.Status{Enabled: true, Connected: true, Host: "bind", Port: 953},
		create: provision.Result{Zone: "new.example.", Serial: &serial, CatalogAdded: true, SeedMode: "shared_dir"},
		delete: provision.Result{Zone: "new.example.", CatalogRemoved: true},
	}
	h := httpapi.New(httpapi.Deps{
		Settings:    settings,
		Auth:        auth.NewCombined(settings),
		Provisioner: fp,
		DNSReady:    func(context.Context) bool { return true },
		StoreReady:  func(context.Context) bool { return true },
	})
	code, body, _ := doJSON(t, h, http.MethodGet, "/v1/rndc/status")
	if code != 200 || body["enabled"] != true {
		t.Fatalf("status %d %v", code, body)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/zones", bytes.NewReader([]byte(`{"zone":"new.example"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rr.Code, rr.Body.String())
	}
	if len(fp.created) != 1 || fp.created[0].Zone != "new.example" {
		t.Fatalf("created=%+v", fp.created)
	}

	del := httptest.NewRequest(http.MethodDelete, "/v1/zones/new.example.", nil)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, del)
	if rr2.Code != http.StatusOK {
		t.Fatalf("delete %d %s", rr2.Code, rr2.Body.String())
	}
	if len(fp.deleted) != 1 {
		t.Fatalf("deleted=%v", fp.deleted)
	}
}

func TestAddAndRemoveZoneCatalog(t *testing.T) {
	settings := testSettings()
	fp := &fakeProvisioner{
		status: provision.Status{Enabled: true, Connected: true, CatalogEnabled: true},
	}
	h := httpapi.New(httpapi.Deps{
		Settings:    settings,
		Auth:        auth.NewCombined(settings),
		Provisioner: fp,
		DNSReady:    func(context.Context) bool { return true },
		StoreReady:  func(context.Context) bool { return true },
	})

	put := httptest.NewRequest(http.MethodPut, "/v1/zones/staged.example./catalog", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, put)
	if rr.Code != http.StatusOK {
		t.Fatalf("put %d %s", rr.Code, rr.Body.String())
	}
	if len(fp.catalogAdded) != 1 || fp.catalogAdded[0] != "staged.example." {
		t.Fatalf("catalogAdded=%v", fp.catalogAdded)
	}
	var putBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &putBody); err != nil {
		t.Fatal(err)
	}
	if putBody["catalog_added"] != true {
		t.Fatalf("put body=%v", putBody)
	}

	del := httptest.NewRequest(http.MethodDelete, "/v1/zones/staged.example./catalog", nil)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, del)
	if rr2.Code != http.StatusOK {
		t.Fatalf("delete catalog %d %s", rr2.Code, rr2.Body.String())
	}
	if len(fp.catalogRemoved) != 1 {
		t.Fatalf("catalogRemoved=%v", fp.catalogRemoved)
	}
}

func TestAddZoneCatalogRequiresRNDC(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/v1/zones/x.example./catalog", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
}

type fakeCatalogIndexer struct {
	zones []string
}

func (f *fakeCatalogIndexer) Connected() bool              { return true }
func (f *fakeCatalogIndexer) ZoneName() string             { return "catalog.test." }
func (f *fakeCatalogIndexer) Serial() (uint32, bool)       { return 1, true }
func (f *fakeCatalogIndexer) ListZones() ([]string, error) { return f.zones, nil }
func (f *fakeCatalogIndexer) PollInterval() float64        { return 60 }

func TestListZonesIncludesInCatalog(t *testing.T) {
	settings := testSettings()
	backend := newFakeBackend()
	backend.seed("cataloged.example.", 1)
	backend.seed("local.example.", 1)
	cache := dnsx.NewCacheWithBackend(settings, backend)
	if _, err := cache.LoadZone(context.Background(), "cataloged.example."); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := cache.LoadZone(context.Background(), "local.example."); err != nil {
		t.Fatalf("load: %v", err)
	}
	h := httpapi.New(httpapi.Deps{
		Settings:   settings,
		Auth:       auth.NewCombined(settings),
		Cache:      cache,
		Catalog:    &fakeCatalogIndexer{zones: []string{"cataloged.example."}},
		DNSReady:   func(context.Context) bool { return true },
		StoreReady: func(context.Context) bool { return true },
	})
	code, body, _ := doJSON(t, h, http.MethodGet, "/v1/zones")
	if code != http.StatusOK {
		t.Fatalf("status %d %v", code, body)
	}
	zones, _ := body["zones"].([]any)
	got := map[string]bool{}
	for _, z := range zones {
		m, _ := z.(map[string]any)
		name, _ := m["zone"].(string)
		in, _ := m["in_catalog"].(bool)
		got[name] = in
	}
	if !got["cataloged.example."] || got["local.example."] {
		t.Fatalf("in_catalog map=%v", got)
	}
}
