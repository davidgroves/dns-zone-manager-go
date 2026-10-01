package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
)

type ctxKey int

const (
	ctxWideEvent ctxKey = iota + 1
	ctxUser
)

func wideEventFrom(ctx context.Context) *logging.WideEvent {
	v, _ := ctx.Value(ctxWideEvent).(*logging.WideEvent)
	return v
}

func userFrom(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(ctxUser).(auth.User)
	return u, ok
}

func withUser(ctx context.Context, u auth.User) context.Context {
	return context.WithValue(ctx, ctxUser, u)
}

// OriginCheck rejects cross-origin mutating requests not on the allowlist.
func OriginCheck(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method := strings.ToUpper(r.Method)
			if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
				origin := r.Header.Get("Origin")
				if origin != "" && !originAllowed(origin, r.Host, allowed) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":   "ForbiddenOrigin",
						"message": "Origin not allowed",
					})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(origin, host string, allowed map[string]struct{}) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if host != "" && strings.EqualFold(u.Host, host) {
		return true
	}
	_, ok := allowed[origin]
	return ok
}

// CORS adds CORS headers when origins are configured.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	allow := strings.Join(allowedOrigins, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				for _, o := range allowedOrigins {
					if o == origin || o == "*" {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Access-Control-Allow-Credentials", "true")
						w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
						w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
						w.Header().Add("Vary", "Origin")
						break
					}
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_ = allow
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBytes limits request body size.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	if n <= 0 {
		n = 10 << 20
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WideEvent middleware emits structured request logs and sets X-Request-ID.
func WideEvent(sampleRate float64, slowThresholdMS float64) func(http.Handler) http.Handler {
	exclude := map[string]struct{}{
		"/health": {}, "/ready": {}, "/metrics": {}, "/docs": {}, "/openapi.json": {}, "/openapi.yaml": {},
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ev := logging.NewWideEvent()
			w.Header().Set("X-Request-ID", ev.RequestID)

			if _, skip := exclude[r.URL.Path]; skip || strings.HasPrefix(r.URL.Path, "/static") {
				next.ServeHTTP(w, r)
				return
			}

			clientIP := r.RemoteAddr
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				clientIP = strings.TrimSpace(strings.Split(fwd, ",")[0])
			}
			q := map[string]string{}
			for k, vs := range r.URL.Query() {
				if len(vs) > 0 {
					q[k] = vs[0]
				}
			}
			ev.SetRequest(r.Method, r.URL.Path, clientIP, r.UserAgent(), q)

			ww := &statusWriter{ResponseWriter: w, status: 200}
			ctx := context.WithValue(r.Context(), ctxWideEvent, ev)
			start := time.Now()
			next.ServeHTTP(ww, r.WithContext(ctx))
			dur := float64(time.Since(start).Microseconds()) / 1000.0
			outcome := "success"
			if ww.status >= 400 {
				outcome = "error"
			}
			ev.SetResponse(ww.status, dur, outcome)
			if ev.ShouldSample(sampleRate, slowThresholdMS) {
				ev.Emit(logging.Default())
			}
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = 200
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap supports http.ResponseController / Flusher.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
