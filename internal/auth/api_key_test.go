package auth_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

func TestAPIKeyValidate(t *testing.T) {
	var secret config.Secret
	secret.Set("test-secret-key")
	a := &auth.APIKey{Settings: config.APIKeySettings{
		Enabled: true,
		Keys:    map[string]config.Secret{"admin": secret},
	}}

	u, err := a.Validate("test-secret-key")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "admin" || u.AuthType != "api_key" {
		t.Fatalf("got %+v", u)
	}

	_, err = a.Validate("wrong")
	if !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Fatalf("expected ErrInvalidAPIKey, got %v", err)
	}

	u, err = a.Validate("")
	if err != nil || !u.Zero() {
		t.Fatalf("empty key should be zero, got %+v %v", u, err)
	}
}

func TestAPIKeyDisabled(t *testing.T) {
	a := &auth.APIKey{Settings: config.APIKeySettings{Enabled: false}}
	u, err := a.Validate("anything")
	if err != nil || !u.Zero() {
		t.Fatalf("disabled should return zero: %+v %v", u, err)
	}
}

func TestCombinedAnonymousWhenDisabled(t *testing.T) {
	c := &auth.Combined{}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	u, err := c.Authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	if u.AuthType != "none" || u.ID != "anonymous" {
		t.Fatalf("got %+v", u)
	}
}

func TestCombinedAPIKey(t *testing.T) {
	var secret config.Secret
	secret.Set("k1")
	c := &auth.Combined{
		APIKey: config.APIKeySettings{
			Enabled:    true,
			HeaderName: "X-API-Key",
			Keys:       map[string]config.Secret{"ops": secret},
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "k1")
	u, err := c.Authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "ops" || u.AuthType != "api_key" {
		t.Fatalf("got %+v", u)
	}
}

func TestCombinedUnauthorized(t *testing.T) {
	c := &auth.Combined{
		APIKey: config.APIKeySettings{Enabled: true, Keys: map[string]config.Secret{}},
	}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	_, err := c.Authenticate(req)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProxyAuth(t *testing.T) {
	p := &auth.Proxy{Settings: config.ProxyAuthSettings{
		Enabled:    true,
		UserHeader: "X-Auth-Request-Email",
		NameHeader: "X-Auth-Request-Preferred-Username",
	}}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Auth-Request-Email", "alice@example.com")
	req.Header.Set("X-Auth-Request-Preferred-Username", "Alice")
	u, err := p.Authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.com" || u.Name != "Alice" || u.AuthType != "proxy" {
		t.Fatalf("got %+v", u)
	}
}
