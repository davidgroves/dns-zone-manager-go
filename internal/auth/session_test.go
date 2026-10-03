package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
)

func TestSessionStoreIssueLookupRevoke(t *testing.T) {
	s := auth.NewSessionStore()
	u := auth.User{ID: "ops", AuthType: "api_key"}
	token, ttl, err := s.Issue(u, true)
	if err != nil {
		t.Fatal(err)
	}
	if ttl != 30*24*time.Hour {
		t.Fatalf("ttl=%v", ttl)
	}
	got, ok := s.Lookup(token)
	if !ok || got.ID != "ops" {
		t.Fatalf("lookup %+v ok=%v", got, ok)
	}
	if _, ok := s.Lookup("deadbeef"); ok {
		t.Fatal("unknown token")
	}
	s.Revoke(token)
	if _, ok := s.Lookup(token); ok {
		t.Fatal("revoked token still valid")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	auth.WriteSessionCookie(rr, req, "tok", 30*24*time.Hour)
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	c := cookies[0]
	if c.Name != auth.SessionCookieName || c.Value != "tok" {
		t.Fatalf("cookie %+v", c)
	}
	if !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("flags %+v", c)
	}
	if c.MaxAge != int((30 * 24 * time.Hour).Seconds()) {
		t.Fatalf("MaxAge=%d", c.MaxAge)
	}
}
