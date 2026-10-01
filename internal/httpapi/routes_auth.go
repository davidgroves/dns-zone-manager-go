package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

func registerAuth(api huma.API, d *Deps) {
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
		OperationID: "auth-logout",
		Method:      http.MethodPost,
		Path:        "/v1/auth/logout",
		Summary:     "Record logout",
		Tags:        []string{"Auth"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body map[string]string
	}, error) {
		metrics.IncLogouts()
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
}
