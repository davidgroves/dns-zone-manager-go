package live

import (
	"net/http"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

// WithAPIKeyQuery copies api_key from the query string into the API-key header
// when the header is empty. Non-browser clients can still authenticate that way;
// the SPA uses the HttpOnly session cookie instead.
func WithAPIKeyQuery(r *http.Request, settings *config.Settings) *http.Request {
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
	return r
}

// AuthenticateWS authenticates a WebSocket upgrade using headers, cookies, and query params.
func AuthenticateWS(r *http.Request, settings *config.Settings) (auth.User, error) {
	if settings == nil {
		settings = &config.Settings{}
	}
	r = WithAPIKeyQuery(r, settings)
	return auth.NewCombined(settings).Authenticate(r)
}

// AuthenticateWSRequest is like AuthenticateWS but also attaches change context.
func AuthenticateWSRequest(r *http.Request, settings *config.Settings) (auth.User, *http.Request, error) {
	if settings == nil {
		settings = &config.Settings{}
	}
	r = WithAPIKeyQuery(r, settings)
	return auth.NewCombined(settings).AuthenticateRequest(r)
}
