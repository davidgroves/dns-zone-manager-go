package httpapi

import (
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/davidgroves/dns-zone-manager-go/internal/live"
)

func registerLive(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("GET /v1/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWS(w, r, d, "*")
	})
	mux.HandleFunc("GET /v1/zones/{zone}/ws", func(w http.ResponseWriter, r *http.Request) {
		zone := normalizeZone(r.PathValue("zone"))
		serveWS(w, r, d, zone)
	})
}

func serveWS(w http.ResponseWriter, r *http.Request, d *Deps, zone string) {
	if d.Hub == nil {
		http.Error(w, "live updates not available", http.StatusServiceUnavailable)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		allowed := map[string]struct{}{}
		for _, o := range d.Settings.Server.CorsOrigins {
			allowed[o] = struct{}{}
		}
		if !originAllowed(origin, r.Host, allowed) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
	}
	if _, _, err := live.AuthenticateWSRequest(r, d.Settings); err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	clientIP := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		clientIP = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Origin already checked above
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	subZone := zone
	if subZone == "" {
		subZone = "*"
	}
	_ = conn.Write(r.Context(), websocket.MessageText,
		[]byte(`{"type":"subscribed","zone":"`+subZone+`"}`))

	if err := d.Hub.Accept(r.Context(), conn, zone, clientIP); err != nil {
		if live.IsConnectionLimit(err) {
			_ = conn.Close(1013, live.LimitReason(err))
			return
		}
	}
}
