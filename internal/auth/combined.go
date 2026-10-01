package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
)

// ErrUnauthorized is returned when auth is required but no valid credentials were supplied.
var ErrUnauthorized = errors.New("authentication required")

// User is an authenticated caller from any auth method.
type User struct {
	ID       string
	Name     string
	Email    string
	AuthType string // "api_key", "proxy", or "none"
	Roles    []string
}

// Zero reports whether u is an empty (unauthenticated) user.
func (u User) Zero() bool {
	return u.ID == "" && u.AuthType == ""
}

// Combined tries trusted proxy headers first, then API key.
type Combined struct {
	APIKey config.APIKeySettings
	Proxy  config.ProxyAuthSettings
}

// NewCombined builds a Combined authenticator from settings.
func NewCombined(settings *config.Settings) *Combined {
	if settings == nil {
		return &Combined{}
	}
	return &Combined{
		APIKey: settings.APIKey,
		Proxy:  settings.ProxyAuth,
	}
}

// Authenticate resolves the caller identity.
//
// If neither auth method is enabled, returns an anonymous admin user.
// On success, sets the change-context actor fields on the request context
// via notifications.WithChangeContext — callers should use the returned
// request from AuthenticateRequest when they need the enriched context.
func (c *Combined) Authenticate(r *http.Request) (User, error) {
	user, err := c.authenticate(r)
	return user, err
}

// AuthenticateRequest is like Authenticate but also attaches change context
// to a derived request context for DNS attribution.
func (c *Combined) AuthenticateRequest(r *http.Request) (User, *http.Request, error) {
	user, err := c.authenticate(r)
	if err != nil {
		return User{}, r, err
	}
	cc := notifications.ChangeContext{
		Actor:      strPtr(user.ID),
		ActorName:  strPtr(user.Name),
		ActorEmail: strPtr(user.Email),
		AuthType:   strPtr(user.AuthType),
		Trigger:    notifications.TriggerManual,
	}
	if user.Name == "" {
		cc.ActorName = nil
	}
	if user.Email == "" {
		cc.ActorEmail = nil
	}
	ctx := notifications.WithChangeContext(r.Context(), cc)
	return user, r.WithContext(ctx), nil
}

func (c *Combined) authenticate(r *http.Request) (User, error) {
	if !c.APIKey.Enabled && !c.Proxy.Enabled {
		return User{
			ID:       "anonymous",
			Name:     "Anonymous",
			AuthType: "none",
		}, nil
	}

	if c.Proxy.Enabled {
		proxy := &Proxy{Settings: c.Proxy}
		if u, err := proxy.Authenticate(r); err != nil {
			return User{}, err
		} else if !u.Zero() {
			return u, nil
		}
	}

	if c.APIKey.Enabled {
		api := &APIKey{Settings: c.APIKey}
		header := c.APIKey.HeaderName
		if header == "" {
			header = "X-API-Key"
		}
		key := r.Header.Get(header)
		if key != "" {
			u, err := api.Validate(key)
			if err != nil {
				return User{}, err
			}
			if !u.Zero() {
				return u, nil
			}
		}
	}

	methods := make([]string, 0, 2)
	if c.Proxy.Enabled {
		h := c.Proxy.UserHeader
		if h == "" {
			h = "X-Auth-Request-Email"
		}
		methods = append(methods, fmt.Sprintf("Trusted proxy header (%s)", h))
	}
	if c.APIKey.Enabled {
		h := c.APIKey.HeaderName
		if h == "" {
			h = "X-API-Key"
		}
		methods = append(methods, fmt.Sprintf("API Key (%s header)", h))
	}
	return User{}, fmt.Errorf("%w. Supported methods: %s", ErrUnauthorized, strings.Join(methods, ", "))
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
