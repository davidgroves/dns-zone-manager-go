package httpapi

import (
	"net/http"
	"strings"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
)

// publicPaths do not require authentication.
var publicPaths = map[string]struct{}{
	"/":               {},
	"/health":         {},
	"/ready":          {},
	"/metrics":        {},
	"/ui/config":      {},
	"/ui/logo":        {},
	"/docs":           {},
	"/openapi":        {},
	"/openapi.json":   {},
	"/openapi.yaml":   {},
	"/v1/auth/login":  {},
	"/v1/auth/logout": {},
}

func isPublicPath(path string) bool {
	if _, ok := publicPaths[path]; ok {
		return true
	}
	if strings.HasPrefix(path, "/docs") || strings.HasPrefix(path, "/schemas") {
		return true
	}
	// SPA assets
	if path == "/" || strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/ui/") {
		return true
	}
	return false
}

// RequireAuth authenticates requests and puts the user into context.
// Unauthenticated access is allowed for publicPaths; protected routes get 401.
func RequireAuth(a *auth.Combined) func(http.Handler) http.Handler {
	if a == nil {
		a = &auth.Combined{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// WebSocket upgrades authenticate in their own handlers.
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				next.ServeHTTP(w, r)
				return
			}
			if isPublicPath(r.URL.Path) {
				// Still try to attach identity when present (e.g. ui/config).
				if u, err := a.Authenticate(r); err == nil && !u.Zero() {
					if ev := wideEventFrom(r.Context()); ev != nil {
						ev.SetUser(u.ID, u.AuthType, u.Name, u.Email, u.Roles)
					}
					r = r.WithContext(withUser(r.Context(), u))
				}
				next.ServeHTTP(w, r)
				return
			}

			u, nr, err := a.AuthenticateRequest(r)
			if err != nil {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"type":"unauthorized","title":"Unauthorized","status":401,"detail":"authentication required"}`))
				return
			}
			if ev := wideEventFrom(nr.Context()); ev != nil {
				ev.SetUser(u.ID, u.AuthType, u.Name, u.Email, u.Roles)
			}
			next.ServeHTTP(w, nr.WithContext(withUser(nr.Context(), u)))
		})
	}
}

func requireUser(ctx interface{ Value(any) any }) (auth.User, error) {
	u, ok := ctx.Value(ctxUser).(auth.User)
	if !ok || u.Zero() {
		return auth.User{}, unauthorized("authentication required")
	}
	return u, nil
}

func enrichDNS(ctx interface{ Value(any) any }, kv map[string]any) {
	ev, _ := ctx.Value(ctxWideEvent).(*logging.WideEvent)
	if ev != nil {
		ev.SetDNS(kv)
	}
}
