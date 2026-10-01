package auth

import (
	"net/http"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

// Proxy authenticates via trusted reverse-proxy identity headers.
type Proxy struct {
	Settings config.ProxyAuthSettings
}

// Authenticate extracts the proxy-authenticated user from request headers.
// Returns (zero, nil) when proxy auth is disabled or the identity header is absent.
func (p *Proxy) Authenticate(r *http.Request) (User, error) {
	if !p.Settings.Enabled {
		return User{}, nil
	}
	userHeader := p.Settings.UserHeader
	if userHeader == "" {
		userHeader = "X-Auth-Request-Email"
	}
	email := r.Header.Get(userHeader)
	if email == "" {
		return User{}, nil
	}
	name := email
	if p.Settings.NameHeader != "" {
		if n := r.Header.Get(p.Settings.NameHeader); n != "" {
			name = n
		}
	}
	return User{
		ID:       email,
		Name:     name,
		Email:    email,
		AuthType: "proxy",
		Roles:    []string{"proxy_user"},
	}, nil
}
