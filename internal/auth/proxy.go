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
// Returns (zero, nil) when proxy auth is disabled or no identity header is present.
//
// Identity resolution (first non-empty wins for the user id/email):
//  1. configured user_header (default X-Auth-Request-Email)
//  2. X-Auth-Request-Preferred-Username (Entra tokens often omit email)
//  3. X-Auth-Request-User
func (p *Proxy) Authenticate(r *http.Request) (User, error) {
	if !p.Settings.Enabled {
		return User{}, nil
	}
	userHeader := p.Settings.UserHeader
	if userHeader == "" {
		userHeader = "X-Auth-Request-Email"
	}
	identity := r.Header.Get(userHeader)
	if identity == "" {
		identity = r.Header.Get("X-Auth-Request-Preferred-Username")
	}
	if identity == "" {
		identity = r.Header.Get("X-Auth-Request-User")
	}
	if identity == "" {
		return User{}, nil
	}
	name := identity
	if p.Settings.NameHeader != "" {
		if n := r.Header.Get(p.Settings.NameHeader); n != "" {
			name = n
		}
	}
	return User{
		ID:       identity,
		Name:     name,
		Email:    identity,
		AuthType: "proxy",
		Roles:    []string{"proxy_user"},
	}, nil
}
