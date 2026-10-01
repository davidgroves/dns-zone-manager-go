package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

// ErrInvalidAPIKey is returned when an API key is present but does not match.
var ErrInvalidAPIKey = errors.New("invalid API key")

// APIKey authenticates requests using a configured shared secret header.
type APIKey struct {
	Settings config.APIKeySettings
}

// Authenticate validates the API key from the configured header.
// Returns (zero, nil) when API key auth is disabled or no key is provided.
func (a *APIKey) Authenticate(r *http.Request) (User, error) {
	if !a.Settings.Enabled {
		return User{}, nil
	}
	header := a.Settings.HeaderName
	if header == "" {
		header = "X-API-Key"
	}
	key := r.Header.Get(header)
	if key == "" {
		return User{}, nil
	}
	return a.Validate(key)
}

// Validate checks a raw API key string against configured keys using
// constant-time comparison.
func (a *APIKey) Validate(apiKey string) (User, error) {
	if !a.Settings.Enabled {
		return User{}, nil
	}
	if apiKey == "" {
		return User{}, nil
	}
	for name, secret := range a.Settings.Keys {
		want := secret.String()
		if len(apiKey) != len(want) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(want)) == 1 {
			return User{
				ID:       name,
				Name:     "API Key: " + name,
				AuthType: "api_key",
				Roles:    []string{"api_key_user"},
			}, nil
		}
	}
	return User{}, ErrInvalidAPIKey
}
