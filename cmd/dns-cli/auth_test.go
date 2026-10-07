package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHostsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DNS_CLI_CONFIG_DIR", dir)

	c := hostCredentials{
		Token:        "tok",
		RefreshToken: "rt",
		User:         "user1@example.test",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Second),
		Issuer:       "http://issuer/v2.0",
		ClientID:     "cli-id",
		Scope:        "api://x/access_as_user",
		TokenURL:     "http://issuer/token",
	}
	if err := storeHost("http://localhost:8080", c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := lookupHost("http://localhost:8080/v1/zones")
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if got.Token != "tok" || got.User != c.User || got.ClientID != c.ClientID {
		t.Fatalf("got %+v", got)
	}
	path := filepath.Join(dir, "hosts.yml")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("hosts.yml perms %v; want 0600", fi.Mode().Perm())
	}
	if err := deleteHost("http://localhost:8080"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = lookupHost("http://localhost:8080")
	if err != nil || ok {
		t.Fatalf("expected deleted, ok=%v err=%v", ok, err)
	}
}

func TestAuthLoginWithToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DNS_CLI_CONFIG_DIR", dir)
	opts := &cliOptions{baseURL: "http://localhost:8080"}

	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"upn":"user1@dns-zone-manager.test","exp":9999999999}`))
	token := "eyJhbGciOiJub25lIn0." + payload + ".x"
	r, w, _ := os.Pipe()
	old := os.Stdin
	os.Stdin = r
	_, _ = w.WriteString(token + "\n")
	_ = w.Close()
	defer func() { os.Stdin = old }()

	if err := authLoginWithToken(opts); err != nil {
		t.Fatal(err)
	}
	c, ok, err := lookupHost(opts.baseURL)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if c.Token != token || c.User != "user1@dns-zone-manager.test" {
		t.Fatalf("got %+v", c)
	}
}

func TestAuthLoginDeviceAndBearerHeader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DNS_CLI_CONFIG_DIR", dir)

	var polls atomic.Int32
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"upn":"user1@dns-zone-manager.test","exp":9999999999,"aud":"api://dns-zone-manager"}`))
	access := "eyJhbGciOiJub25lIn0." + payload + ".sig"

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                        "http://" + r.Host + "/t/v2.0",
				"token_endpoint":                "http://" + r.Host + "/token",
				"device_authorization_endpoint": "http://" + r.Host + "/devicecode",
			})
		case r.URL.Path == "/devicecode":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dc",
				"user_code":        "ABCD-EFGH",
				"verification_uri": "http://" + r.Host + "/verify",
				"expires_in":       60,
				"interval":         1,
				"message":          "enter the code",
			})
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" {
				if polls.Add(1) < 2 {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  access,
					"refresh_token": "rt1",
					"expires_in":    3600,
					"token_type":    "Bearer",
				})
				return
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  access,
					"refresh_token": "rt2",
					"expires_in":    3600,
					"token_type":    "Bearer",
				})
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.Close)

	var gotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
	}))
	t.Cleanup(api.Close)

	opts := &cliOptions{baseURL: api.URL}
	if err := authLoginDevice(opts, issuer.URL+"/t/v2.0", "cli-client", "api://dns-zone-manager/access_as_user offline_access"); err != nil {
		t.Fatal(err)
	}
	if polls.Load() < 2 {
		t.Fatalf("expected pending then success, polls=%d", polls.Load())
	}

	client, err := newAPIClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	resp, _, err := client.do(http.MethodGet, "/v1/auth/validate", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if gotAuth != "Bearer "+access {
		t.Fatalf("Authorization=%q", gotAuth)
	}

	stdout, _, err := runCLI(t, opts, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "user1@dns-zone-manager.test") {
		t.Fatalf("status output: %s", stdout)
	}

	_, _, err = runCLI(t, opts, "auth", "logout")
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := lookupHost(api.URL)
	if err != nil || ok {
		t.Fatalf("expected logout, ok=%v err=%v", ok, err)
	}
}

func TestRefreshAccessToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DNS_CLI_CONFIG_DIR", dir)

	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"upn":"user2@dns-zone-manager.test","exp":9999999999}`))
	access := "eyJhbGciOiJub25lIn0." + payload + ".sig"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-rt" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  access,
			"refresh_token": "new-rt",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(srv.Close)

	c := hostCredentials{
		Token:        "expired",
		RefreshToken: "old-rt",
		ClientID:     "cli",
		TokenURL:     srv.URL,
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	got, err := refreshAccessToken(c)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != access || got.RefreshToken != "new-rt" || got.User != "user2@dns-zone-manager.test" {
		t.Fatalf("got %+v", got)
	}
}
