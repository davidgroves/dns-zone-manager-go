//go:build integration

package integration_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if !dockerAvailable() {
		os.Stderr.WriteString("SKIP: Docker not available for integration tests\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func dockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

func TestBINDHealthReadyAndZoneLoad(t *testing.T) {
	stack := startBINDAPI(t, stackOptions{})

	t.Run("health", func(t *testing.T) {
		rr := httptest.NewRecorder()
		stack.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), `"status"`)
		require.NotEmpty(t, rr.Header().Get("X-Request-ID"))
	})

	t.Run("ready", func(t *testing.T) {
		rr := httptest.NewRecorder()
		stack.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("list_zones", func(t *testing.T) {
		rr := httptest.NewRecorder()
		stack.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/zones?limit=10&offset=0", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), "test.example.")
		require.Contains(t, rr.Body.String(), "total_count")
	})

	t.Run("add_rrset", func(t *testing.T) {
		body := `{"name":"api-test","type":"A","ttl":300,"records":["192.0.2.99"]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/zones/test.example./rrsets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		stack.Handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	})
}
