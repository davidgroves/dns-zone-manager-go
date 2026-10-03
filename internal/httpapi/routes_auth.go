package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

func registerAuth(api huma.API, mux *http.ServeMux, d *Deps) {
	type loginIn struct {
		Body struct {
			AuthType string `json:"auth_type" enum:"api_key,proxy"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID:   "auth-login",
		Method:        http.MethodPost,
		Path:          "/v1/auth/login",
		Summary:       "Record login",
		Tags:          []string{"Auth"},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *loginIn) (*struct {
		Body map[string]string
	}, error) {
		metrics.IncLogins(in.Body.AuthType)
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "auth-validate",
		Method:      http.MethodGet,
		Path:        "/v1/auth/validate",
		Summary:     "Validate credentials",
		Tags:        []string{"Auth"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body map[string]any
	}, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string]any{
			"valid":     true,
			"user_id":   u.ID,
			"auth_type": u.AuthType,
		}
		if u.Name != "" {
			out["name"] = u.Name
		}
		return &struct{ Body map[string]any }{Body: out}, nil
	})

	mux.HandleFunc("POST /v1/auth/session", handleCreateSession(d))
	mux.HandleFunc("POST /v1/auth/logout", handleLogout(d))
}

func handleCreateSession(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFrom(r.Context())
		if !ok || u.Zero() || u.AuthType == "none" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"unauthorized","title":"Unauthorized","status":401,"detail":"authentication required"}`))
			return
		}
		if d.Auth == nil || d.Auth.Sessions == nil {
			http.Error(w, `{"detail":"sessions unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Remember bool `json:"remember"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		token, ttl, err := d.Auth.Sessions.Issue(u, body.Remember)
		if err != nil {
			http.Error(w, `{"detail":"could not create session"}`, http.StatusInternalServerError)
			return
		}
		auth.WriteSessionCookie(w, r, token, ttl)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "remember": body.Remember})
	}
}

func handleLogout(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth != nil && d.Auth.Sessions != nil {
			if ck, err := r.Cookie(auth.SessionCookieName); err == nil {
				d.Auth.Sessions.Revoke(ck.Value)
			}
		}
		auth.ClearSessionCookie(w, r)
		metrics.IncLogouts()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
