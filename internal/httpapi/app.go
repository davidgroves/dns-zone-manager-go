package httpapi

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/version"
)

// New builds the fully wired HTTP handler for the DNS Zone Manager API.
func New(deps Deps) http.Handler {
	if deps.Settings == nil {
		panic("httpapi.New: Settings required")
	}
	if deps.Auth == nil {
		deps.Auth = auth.NewCombined(deps.Settings)
	}

	mux := http.NewServeMux()
	cfg := huma.DefaultConfig(deps.Settings.AppName, version.Current())
	cfg.DocsPath = "/docs"
	cfg.OpenAPIPath = "/openapi"
	api := humago.New(mux, cfg)

	registerMeta(api, mux, &deps)
	registerAuth(api, &deps)
	registerZones(api, mux, &deps)
	registerRRsets(api, &deps)
	registerAtomic(api, &deps)
	registerSearch(api, &deps)
	registerNSUpdate(api, mux, &deps)
	registerCatalog(api, &deps)
	registerReverse(api, &deps)
	registerHistory(api, &deps)
	registerScheduled(api, &deps)
	registerLive(mux, &deps)

	sample := deps.Settings.Logging.SampleRate
	if sample <= 0 {
		sample = 0.1
	}
	slow := float64(deps.Settings.Logging.SlowThresholdMS)
	if slow <= 0 {
		slow = 1000
	}

	h := newSPAFallback(mux, deps.UIDir)
	h = RequireAuth(deps.Auth)(h)
	h = WideEvent(sample, slow)(h)
	h = OriginCheck(deps.Settings.Server.CorsOrigins)(h)
	h = CORS(deps.Settings.Server.CorsOrigins)(h)
	h = MaxBytes(deps.Settings.Server.MaxBodyBytes)(h)
	return h
}

// ServerTimeouts extracts ListenAndServe timeouts from settings.
func ServerTimeouts(deps Deps) (readHeader, read, idle, shutdown time.Duration) {
	s := deps.Settings.Server
	return s.ReadHeaderTimeout, s.ReadTimeout, s.IdleTimeout, s.ShutdownTimeout
}
