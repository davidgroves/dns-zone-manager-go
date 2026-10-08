//go:build integration

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/catalog"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/httpapi"
	"github.com/davidgroves/dns-zone-manager-go/internal/live"
	"github.com/davidgroves/dns-zone-manager-go/internal/provision"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

// bindAPIStack is a BIND container plus an HTTP API wired to it.
type bindAPIStack struct {
	Zone        string
	CatalogZone string
	BaseURL     string // set when serveHTTP is true
	Handler     http.Handler
	Settings    *config.Settings
	Client      *dnsx.Client
	Cache       *dnsx.ZoneCache
	Store       *store.Store
	Server      *httptest.Server
	ZoneDir     string
	Provisioner httpapi.ZoneProvisioner
}

type stackOptions struct {
	withStore bool
	serveHTTP bool
	withRNDC  bool
}

// startBINDAPI starts BIND via testcontainers, loads the test zone, and builds
// the API handler. When withStore is true, SQLite scheduling is enabled.
// When serveHTTP is true, an httptest.Server is started and BaseURL is set.
func startBINDAPI(t *testing.T, opts stackOptions) *bindAPIStack {
	t.Helper()
	ctx := context.Background()

	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	secretB64 := base64.StdEncoding.EncodeToString(secret)
	rndcSecret := make([]byte, 32)
	_, err = rand.Read(rndcSecret)
	require.NoError(t, err)
	rndcSecretB64 := base64.StdEncoding.EncodeToString(rndcSecret)
	keyName := "integration-test-key"
	zone := "test.example."

	dir, hostDir := bindConfigDir(t)
	writeBindConfig(t, dir, keyName, secretB64, zone, bindExtra{withRNDC: opts.withRNDC, rndcSecretB64: rndcSecretB64})

	ports := []string{"53/tcp", "53/udp"}
	if opts.withRNDC {
		ports = append(ports, "953/tcp")
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "internetsystemsconsortium/bind9:9.20",
			ExposedPorts: ports,
			Env:          map[string]string{"TZ": "UTC", "BIND9_USER": "root"},
			WaitingFor:   wait.ForListeningPort("53/tcp").WithStartupTimeout(90 * time.Second),
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Binds = []string{
					filepath.Join(hostDir, "named.conf") + ":/etc/bind/named.conf:ro",
					filepath.Join(hostDir, "zones") + ":/etc/bind/zones:rw",
				}
			},
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	tcpPort, err := c.MappedPort(ctx, "53/tcp")
	require.NoError(t, err)
	udpPort, err := c.MappedPort(ctx, "53/udp")
	require.NoError(t, err)
	rndcPort := ""
	if opts.withRNDC {
		p, err := c.MappedPort(ctx, "953/tcp")
		require.NoError(t, err)
		rndcPort = p.Port()
	}

	dbPath := ""
	if opts.withStore {
		dbPath = filepath.Join(t.TempDir(), "scheduler.db")
	}
	cfgPath := writeAppConfig(t, appConfigParams{
		host: host, udpPort: udpPort.Port(), tcpPort: tcpPort.Port(),
		keyName: keyName, secretB64: secretB64,
		withStore: opts.withStore, dbPath: dbPath,
		withRNDC: opts.withRNDC, rndcPort: rndcPort, rndcSecretB64: rndcSecretB64,
		zoneDir: filepath.Join(dir, "zones"),
	})
	settings, err := config.Load(cfgPath)
	require.NoError(t, err)

	client, err := dnsx.NewClient(settings)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.Eventually(t, func() bool {
		return client.CheckServerResponding(ctx)
	}, 45*time.Second, 500*time.Millisecond, "BIND not responding")

	cache := dnsx.NewCache(settings, client)
	cz, err := cache.LoadZone(ctx, zone)
	require.NoError(t, err)
	require.NotNil(t, cz)
	require.GreaterOrEqual(t, cz.Zone.RRsetCount(), 3)

	var st *store.Store
	if opts.withStore {
		st = store.New(settings.Database, settings.Scheduler.DefaultExpiryWindow)
		require.NoError(t, st.Open(ctx))
		t.Cleanup(func() { _ = st.Close() })
	}

	hub := live.NewHub(live.HubConfig{
		MaxConnections: 10, MaxConnectionsPerIP: 5,
		SendTimeout: time.Second, PingInterval: time.Minute,
	})
	var catIndexer *catalog.Indexer
	var catalogDep httpapi.CatalogIndexer
	var catSrc provision.CatalogSource
	if settings.Catalog.Enabled && settings.Catalog.ZoneName != "" {
		catIndexer = catalog.New(catalog.ConfigFromSettings(settings), client)
		if err := catIndexer.Start(ctx); err != nil {
			t.Logf("catalog indexer start: %v", err)
			catIndexer = nil
		} else {
			t.Cleanup(catIndexer.Stop)
			catalogDep = catIndexer
			catSrc = catIndexer
		}
	}
	var prov httpapi.ZoneProvisioner
	if opts.withRNDC {
		prov = provision.New(settings, client, cache, catSrc, st)
	}
	handler := httpapi.New(httpapi.Deps{
		Settings:    settings,
		Auth:        auth.NewCombined(settings),
		Client:      client,
		Cache:       cache,
		Store:       st,
		Hub:         hub,
		Catalog:     catalogDep,
		Provisioner: prov,
		DNSReady:    func(context.Context) bool { return true },
		StoreReady:  func(context.Context) bool { return st == nil || st.Ping(context.Background()) },
	})

	stack := &bindAPIStack{
		Zone:        zone,
		CatalogZone: "catalog.test.",
		Handler:     handler,
		Settings:    settings,
		Client:      client,
		Cache:       cache,
		Store:       st,
		ZoneDir:     filepath.Join(dir, "zones"),
		Provisioner: prov,
	}
	if opts.serveHTTP {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		stack.Server = srv
		stack.BaseURL = srv.URL
	}
	return stack
}

func bindConfigDir(t *testing.T) (local, host string) {
	t.Helper()
	base := t.TempDir()
	if _, err := os.Stat("/workspace"); err == nil {
		_ = os.MkdirAll("/workspace/.tmp", 0o755)
		if d, err := os.MkdirTemp("/workspace/.tmp", "bind_test_"); err == nil {
			t.Cleanup(func() { _ = os.RemoveAll(d) })
			base = d
		}
	}
	host = base
	if strings.HasPrefix(base, "/workspace") {
		if ws := workspaceHostPath(); ws != "" {
			host = ws + strings.TrimPrefix(base, "/workspace")
		}
	}
	return base, host
}

// workspaceHostPath returns the host path mounted at /workspace so Docker can
// bind-mount config into sibling containers. Honours WORKSPACE_HOST_PATH, then
// falls back to inspecting the current container's mounts.
func workspaceHostPath() string {
	if ws := os.Getenv("WORKSPACE_HOST_PATH"); ws != "" {
		return ws
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return ""
	}
	out, err := exec.Command("docker", "inspect", "-f",
		`{{range .Mounts}}{{if eq .Destination "/workspace"}}{{.Source}}{{end}}{{end}}`,
		hostname,
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func writeBindConfig(t *testing.T, dir, keyName, secretB64, zone string, extra bindExtra) {
	t.Helper()
	rndcBlock := ""
	catalogBlock := ""
	if extra.withRNDC {
		rndcBlock = fmt.Sprintf(`
key "rndc-key" {
    algorithm hmac-sha256;
    secret "%s";
};
controls {
    inet * port 953 allow { any; } keys { "rndc-key"; };
};
`, extra.rndcSecretB64)
		catalogBlock = fmt.Sprintf(`
zone "catalog.test" {
    type primary;
    file "/etc/bind/zones/catalog.test.zone";
    allow-update { key %s; };
    allow-transfer { key %s; };
};
`, keyName, keyName)
	}
	namedConf := fmt.Sprintf(`
options {
    directory "/var/cache/bind";
    listen-on port 53 { any; };
    listen-on-v6 { none; };
    allow-query { any; };
    allow-transfer { any; };
    recursion no;
    dnssec-validation no;
    allow-new-zones %s;
};
key "%s" {
    algorithm hmac-sha256;
    secret "%s";
};
%s
zone "%s" {
    type primary;
    file "/etc/bind/zones/test.example.zone";
    allow-update { key %s; };
    allow-transfer { key %s; };
};
%s
`, boolYes(extra.withRNDC), keyName, secretB64, rndcBlock, zone, keyName, keyName, catalogBlock)

	zoneFile := fmt.Sprintf(`$ORIGIN %s
$TTL 3600
@ IN SOA ns1.test.example. hostmaster.test.example. (
    2024010101 3600 600 86400 3600 )
@ IN NS ns1.test.example.
ns1 IN A 192.0.2.1
www IN A 192.0.2.10
`, zone)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "named.conf"), []byte(namedConf), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "zones"), 0o777))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "zones", "test.example.zone"), []byte(zoneFile), 0o666))
	_ = os.Chmod(filepath.Join(dir, "zones"), 0o777)
	_ = os.Chmod(filepath.Join(dir, "zones", "test.example.zone"), 0o666)
	if extra.withRNDC {
		cat := `$ORIGIN catalog.test.
$TTL 3600
@ IN SOA ns.catalog.test. host.catalog.test. ( 1 3600 600 86400 60 )
@ IN NS invalid.
version IN TXT "2"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "zones", "catalog.test.zone"), []byte(cat), 0o666))
		_ = os.Chmod(filepath.Join(dir, "zones", "catalog.test.zone"), 0o666)
	}
}

func boolYes(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

type bindExtra struct {
	withRNDC      bool
	rndcSecretB64 string
}

type appConfigParams struct {
	host, udpPort, tcpPort, keyName, secretB64 string
	withStore                                  bool
	dbPath                                     string
	withRNDC                                   bool
	rndcPort, rndcSecretB64, zoneDir           string
}

func writeAppConfig(t *testing.T, p appConfigParams) string {
	t.Helper()
	schedulerBlock := `
scheduler:
  enabled: false
`
	databaseBlock := ""
	if p.withStore {
		schedulerBlock = `
scheduler:
  enabled: true
  poll_interval: 1h
  max_attempts: 3
  retry_backoff: 1s
  lease_ttl: 2m
  default_expiry_window: 1h
`
		databaseBlock = fmt.Sprintf(`
database:
  backend: sqlite
  auto_migrate: true
  path: %q
`, p.dbPath)
	}
	catalogBlock := `
catalog:
  enabled: false
`
	rndcBlock := ""
	if p.withRNDC {
		catalogBlock = `
catalog:
  enabled: true
  zone_name: catalog.test.
  poll_interval: 60
  auto_load_zones: false
  remove_stale_zones: false
`
		rndcBlock = fmt.Sprintf(`
rndc:
  enabled: true
  host: %s
  port: %s
  algorithm: hmac-sha256
  secret: %s
  zone_seed:
    mode: shared_dir
    local_dir: %q
    bind_dir: /etc/bind/zones
  zone_defaults:
    primary_ns: ns1.test.example.
    admin_email: hostmaster.test.example.
    nameservers:
      - ns1.test.example.
`, p.host, p.rndcPort, p.rndcSecretB64, p.zoneDir)
	}
	cfgYAML := fmt.Sprintf(`
debug: true
tsig_keys:
  - name: %s
    secret: %s
    algorithm: hmac-sha256
dns:
  server: %s
  port: %s
  tcp_port: %s
  update_tsig_key: %s
  axfr_tsig_key: %s
  timeout: 5
  axfr_timeout: 30
  pool_size: 2
api_key:
  enabled: false
proxy_auth:
  enabled: false
cache:
  enabled: true
notify:
  enabled: false
%s
%s
%s
%s
webhooks:
  enabled: false
logging:
  format: text
  level: INFO
  sample_rate: 1.0
`, p.keyName, p.secretB64, p.host, p.udpPort, p.tcpPort, p.keyName, p.keyName, schedulerBlock, databaseBlock, catalogBlock, rndcBlock)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(cfgYAML), 0o600))
	return path
}
