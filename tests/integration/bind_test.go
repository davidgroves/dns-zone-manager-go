//go:build integration

package integration_test

import (
	"bytes"
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
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/httpapi"
	"github.com/davidgroves/dns-zone-manager-go/internal/live"
)

func TestMain(m *testing.M) {
	if !dockerAvailable() {
		os.Stderr.WriteString("SKIP: Docker not available for integration tests\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func dockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

func TestBINDHealthReadyAndZoneLoad(t *testing.T) {
	ctx := context.Background()

	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	secretB64 := base64.StdEncoding.EncodeToString(secret)
	keyName := "integration-test-key"
	zone := "test.example."

	dir, hostDir := bindConfigDir(t)
	writeBindConfig(t, dir, keyName, secretB64, zone)

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "internetsystemsconsortium/bind9:9.20",
			ExposedPorts: []string{"53/tcp", "53/udp"},
			Env:          map[string]string{"TZ": "UTC", "BIND9_USER": "root"},
			WaitingFor:   wait.ForListeningPort("53/tcp").WithStartupTimeout(90 * time.Second),
			HostConfigModifier: func(hc *container.HostConfig) {
				// Zone dir must be writable so BIND can create journals for DDNS.
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

	cfgPath := writeAppConfig(t, host, udpPort.Port(), tcpPort.Port(), keyName, secretB64)
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

	hub := live.NewHub(live.HubConfig{
		MaxConnections: 10, MaxConnectionsPerIP: 5,
		SendTimeout: time.Second, PingInterval: time.Minute,
	})
	handler := httpapi.New(httpapi.Deps{
		Settings: settings,
		Auth:     auth.NewCombined(settings),
		Client:   client,
		Cache:    cache,
		Hub:      hub,
	})

	t.Run("health", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), `"status"`)
		require.NotEmpty(t, rr.Header().Get("X-Request-ID"))
	})

	t.Run("ready", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("list_zones", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/zones?limit=10&offset=0", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), "test.example.")
		require.Contains(t, rr.Body.String(), "total_count")
	})

	t.Run("add_rrset", func(t *testing.T) {
		body := `{"name":"api-test","type":"A","ttl":300,"records":["192.0.2.99"]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/zones/test.example./rrsets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	})
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
	if ws := os.Getenv("WORKSPACE_HOST_PATH"); ws != "" && strings.HasPrefix(base, "/workspace") {
		host = ws + strings.TrimPrefix(base, "/workspace")
	}
	return base, host
}

func writeBindConfig(t *testing.T, dir, keyName, secretB64, zone string) {
	t.Helper()
	namedConf := fmt.Sprintf(`
options {
    directory "/var/cache/bind";
    listen-on port 53 { any; };
    listen-on-v6 { none; };
    allow-query { any; };
    allow-transfer { any; };
    recursion no;
    dnssec-validation no;
};
key "%s" {
    algorithm hmac-sha256;
    secret "%s";
};
zone "%s" {
    type primary;
    file "/etc/bind/zones/test.example.zone";
    allow-update { key %s; };
    allow-transfer { key %s; };
};
`, keyName, secretB64, zone, keyName, keyName)

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
}

func writeAppConfig(t *testing.T, host, udpPort, tcpPort, keyName, secretB64 string) string {
	t.Helper()
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
scheduler:
  enabled: false
catalog:
  enabled: false
webhooks:
  enabled: false
logging:
  format: text
  level: INFO
  sample_rate: 1.0
`, keyName, secretB64, host, udpPort, tcpPort, keyName, keyName)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(cfgYAML), 0o600))
	return path
}
