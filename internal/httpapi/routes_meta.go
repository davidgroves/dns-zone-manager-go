package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/davidgroves/dns-zone-manager-go/internal/version"
)

func registerMeta(api huma.API, mux *http.ServeMux, d *Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "root",
		Method:      http.MethodGet,
		Path:        "/",
		Summary:     "API Info",
		Tags:        []string{"Info"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body map[string]string
	}, error) {
		return &struct{ Body map[string]string }{Body: map[string]string{
			"name":    d.Settings.AppName,
			"version": version.Current(),
			"docs":    "/docs",
			"health":  "/health",
			"metrics": "/metrics",
			"ready":   "/ready",
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health Check",
		Tags:        []string{"Health"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body map[string]any
	}, error) {
		return &struct{ Body map[string]any }{Body: healthBody(ctx, d, false)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ready",
		Method:      http.MethodGet,
		Path:        "/ready",
		Summary:     "Readiness Check",
		Tags:        []string{"Health"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Status int
		Body   map[string]any
	}, error) {
		body := healthBody(ctx, d, true)
		status := http.StatusOK
		ready, _ := body["ready"].(bool)
		if !ready {
			status = http.StatusServiceUnavailable
		}
		return &struct {
			Status int
			Body   map[string]any
		}{Status: status, Body: body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ui-config",
		Method:      http.MethodGet,
		Path:        "/ui/config",
		Summary:     "UI Configuration",
		Tags:        []string{"Info"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body map[string]any
	}, error) {
		var user any
		if u, ok := userFrom(ctx); ok && !u.Zero() && u.AuthType == "proxy" {
			user = map[string]any{"email": u.Email, "name": u.Name}
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"apiKeyEnabled":    d.Settings.APIKey.Enabled,
			"proxyAuthEnabled": d.Settings.ProxyAuth.Enabled,
			"user":             user,
			"version":          version.Current(),
			"theme":            d.Settings.Theme.ToUIDict(d.Settings.AppName),
		}}, nil
	})

	mux.Handle("GET /metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}))

	mux.HandleFunc("GET /ui/logo", func(w http.ResponseWriter, r *http.Request) {
		logo := d.Settings.Theme.Logo
		if logo == nil || logo.Path == nil || strings.TrimSpace(*logo.Path) == "" {
			metrics.IncUILogoRequests("not_configured")
			http.Error(w, `{"detail":"No local logo configured"}`, http.StatusNotFound)
			return
		}
		path := *logo.Path
		data, err := os.ReadFile(path)
		if err != nil {
			metrics.IncUILogoRequests("not_configured")
			http.Error(w, `{"detail":"No local logo configured"}`, http.StatusNotFound)
			return
		}
		metrics.IncUILogoRequests("served")
		ct := logoMediaType(path)
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data)
	})
}

func healthBody(ctx context.Context, d *Deps, readiness bool) map[string]any {
	dnsOK := d.dnsOK(ctx)
	cached := 0
	if d.Cache != nil {
		cached = d.Cache.Len()
	}
	status := "healthy"
	if !dnsOK {
		status = "degraded"
	}

	out := map[string]any{
		"status":        status,
		"dns_server":    d.Settings.DNS.Server,
		"dns_connected": dnsOK,
		"cache_enabled": d.Settings.Cache.Enabled,
		"cached_zones":  cached,
	}

	if d.Settings.Catalog.Enabled {
		zonesDiscovered := 0
		connected := false
		zoneName := d.Settings.Catalog.ZoneName
		if d.Catalog != nil {
			connected = d.Catalog.Connected()
			zoneName = d.Catalog.ZoneName()
			if zs, err := d.Catalog.ListZones(); err == nil {
				zonesDiscovered = len(zs)
			}
		}
		out["catalog"] = map[string]any{
			"enabled":          true,
			"zone_name":        zoneName,
			"connected":        connected,
			"zones_discovered": zonesDiscovered,
		}
	}

	if d.Settings.Notify.Enabled {
		listening := false
		udp, tcp := d.Settings.Notify.UDPPort, d.Settings.Notify.TCPPort
		if d.Notify != nil {
			listening = d.Notify.Listening()
			udp, tcp = d.Notify.UDPPort(), d.Notify.TCPPort()
		}
		out["notify"] = map[string]any{
			"enabled":      true,
			"listening":    listening,
			"udp_port":     udp,
			"tcp_port":     tcp,
			"prefer_ixfr":  d.Settings.Notify.PreferIXFR,
			"require_tsig": d.Settings.Notify.RequireTSIG,
		}
	}

	storeOK := true
	if d.Settings.Scheduler.Enabled {
		storeOK = d.storeOK(ctx)
		backend := d.Settings.Database.Backend
		out["database"] = map[string]any{
			"backend":   backend,
			"connected": storeOK,
		}
	}

	if readiness {
		ready := dnsOK && storeOK
		out["ready"] = ready
		if !ready {
			out["status"] = "not_ready"
		}
	}
	return out
}

func logoMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
