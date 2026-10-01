package live

import (
	"net/http"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

// AuthenticateWS authenticates a WebSocket upgrade using headers and query params.
//
// Browser WebSocket cannot set custom headers, so API keys may be supplied as
// the api_key query parameter. Proxy identity headers and X-API-Key on the
// upgrade request still work when present.
func AuthenticateWS(r *http.Request, settings *config.Settings) (auth.User, error) {
	if settings == nil {
		settings = &config.Settings{}
	}
	// Copy request so we can inject api_key query into the API key header
	// without mutating the caller's header map unexpectedly when already set.
	headerName := settings.APIKey.HeaderName
	if headerName == "" {
		headerName = "X-API-Key"
	}
	if r.Header.Get(headerName) == "" {
		if key := r.URL.Query().Get("api_key"); key != "" {
			r = r.Clone(r.Context())
			r.Header.Set(headerName, key)
		}
	}
	combined := auth.NewCombined(settings)
	return combined.Authenticate(r)
}

// AuthenticateWSRequest is like AuthenticateWS but also attaches change context.
func AuthenticateWSRequest(r *http.Request, settings *config.Settings) (auth.User, *http.Request, error) {
	if settings == nil {
		settings = &config.Settings{}
	}
	headerName := settings.APIKey.HeaderName
	if headerName == "" {
		headerName = "X-API-Key"
	}
	if r.Header.Get(headerName) == "" {
		if key := r.URL.Query().Get("api_key"); key != "" {
			r = r.Clone(r.Context())
			r.Header.Set(headerName, key)
		}
	}
	combined := auth.NewCombined(settings)
	return combined.AuthenticateRequest(r)
}
