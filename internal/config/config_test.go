package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

func TestLoadMinimalYAML(t *testing.T) {
	s, err := config.Load(filepath.Join("testdata", "minimal.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.DNS.Server != "127.0.0.1" {
		t.Fatalf("dns.server = %q", s.DNS.Server)
	}
	if s.AppName != "DNS Zone Manager" {
		t.Fatalf("app_name = %q", s.AppName)
	}
	if !s.APIKey.Enabled {
		t.Fatal("api_key.enabled should default true")
	}
	if s.DNS.EffectiveTCPPort() != 53 {
		t.Fatalf("tcp port = %d", s.DNS.EffectiveTCPPort())
	}
}

func TestLoadSecretFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "api.key")
	if err := os.WriteFile(secretPath, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	body := `
dns:
  server: 127.0.0.1
api_key:
  keys:
    - name: svc
      secret_file: ` + secretPath + `
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.APIKey.Keys["svc"].String(); got != "from-file" {
		t.Fatalf("secret = %q", got)
	}
	if got := s.APIKey.Keys["svc"].Redacted(); got != "***" {
		t.Fatalf("redacted = %q", got)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("MY_TSIG_SECRET", "expanded-secret")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := `
dns:
  server: 127.0.0.1
  update_tsig_key: k1
tsig_keys:
  - name: k1
    secret: ${MY_TSIG_SECRET}
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.TSIGKeys[0].Secret.String(); got != "expanded-secret" {
		t.Fatalf("secret = %q", got)
	}
}

func TestValidationFailures(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"missing dns.server", filepath.Join("testdata", "missing_dns.yaml"), "dns.server is required"},
		{"bad tsig ref", filepath.Join("testdata", "bad_tsig_ref.yaml"), "unknown key"},
		{"retention draft", filepath.Join("testdata", "retention_bad_status.yaml"), "active statuses"},
		{"webhooks enabled", filepath.Join("testdata", "webhooks_enabled.yaml"), "base_url is required"},
	}
	missing := filepath.Join("testdata", "missing_dns.yaml")
	if err := os.WriteFile(missing, []byte("app_name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(missing) })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(tc.path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestTSIGKeyLookup(t *testing.T) {
	s, err := config.Load(filepath.Join("testdata", "tsig_keys.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	key := s.GetUpdateTSIGKey()
	if key == nil || key.Name != "update-key" {
		t.Fatalf("GetUpdateTSIGKey = %#v", key)
	}
}

func TestDatabaseRedactedURL(t *testing.T) {
	s, err := config.Load(filepath.Join("testdata", "postgres.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	redacted := s.Database.RedactedURL()
	if strings.Contains(redacted, "s3cret") {
		t.Fatalf("password leaked in %q", redacted)
	}
	if !strings.Contains(redacted, "***") {
		t.Fatalf("expected redacted password in %q", redacted)
	}
}

func TestAPIKeysFromEnv(t *testing.T) {
	t.Setenv("API_KEYS", "alice:secret1,bob:secret2")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("dns:\n  server: 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.APIKey.Keys["alice"].String() != "secret1" {
		t.Fatalf("alice key = %q", s.APIKey.Keys["alice"].String())
	}
}

func TestThemeToUIDict(t *testing.T) {
	s, err := config.Load(filepath.Join("testdata", "minimal.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ui := s.Theme.ToUIDict(s.AppName)
	if ui["appName"] != "DNS Zone Manager" {
		t.Fatalf("appName = %v", ui["appName"])
	}
	if ui["defaultMode"] != "dark" {
		t.Fatalf("defaultMode = %v", ui["defaultMode"])
	}
}
