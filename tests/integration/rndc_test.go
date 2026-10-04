//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/scheduler"
)

func TestRNDCCreateDeleteZone(t *testing.T) {
	stack := startBINDAPI(t, stackOptions{withStore: true, serveHTTP: true, withRNDC: true})
	ctx := context.Background()

	code, body := doAPI(t, stack.Handler, http.MethodGet, "/v1/rndc/status", nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, body["enabled"])

	code, body = doAPI(t, stack.Handler, http.MethodPost, "/v1/zones", map[string]any{
		"zone": "managed.test.",
	})
	require.Equal(t, http.StatusCreated, code, "create: %v", body)
	require.Equal(t, "managed.test.", body["zone"])
	require.Equal(t, true, body["catalog_added"])

	require.Eventually(t, func() bool {
		_, err := stack.Client.QuerySOA(ctx, "managed.test.")
		return err == nil
	}, 15*time.Second, 200*time.Millisecond)

	cat, err := stack.Client.PerformAXFR(ctx, "catalog.test.")
	require.NoError(t, err)
	require.Contains(t, catalogPTRTargets(cat), "managed.test.")

	code, list := doAPI(t, stack.Handler, http.MethodGet, "/v1/zones", nil)
	require.Equal(t, http.StatusOK, code)
	found := false
	if zones, ok := list["zones"].([]any); ok {
		for _, z := range zones {
			m, _ := z.(map[string]any)
			if m["zone"] == "managed.test." {
				found = true
			}
		}
	}
	require.True(t, found, "created zone not in list: %v", list)

	code, body = doAPI(t, stack.Handler, http.MethodDelete, "/v1/zones/managed.test.", nil)
	require.Equal(t, http.StatusOK, code, "delete: %v", body)

	require.Eventually(t, func() bool {
		_, err := stack.Client.QuerySOA(ctx, "managed.test.")
		return err != nil
	}, 15*time.Second, 200*time.Millisecond)

	code, body = doAPI(t, stack.Handler, http.MethodDelete, "/v1/zones/managed.test.", nil)
	require.Equal(t, http.StatusNotFound, code, "second delete: %v", body)

	cat, err = stack.Client.PerformAXFR(ctx, "catalog.test.")
	require.NoError(t, err)
	require.NotContains(t, catalogPTRTargets(cat), "managed.test.")
}

func TestRNDCScheduledCreate(t *testing.T) {
	stack := startBINDAPI(t, stackOptions{withStore: true, serveHTTP: true, withRNDC: true})
	at := time.Now().UTC().Add(-time.Second)
	code, body := doAPI(t, stack.Handler, http.MethodPost, "/v1/zones", map[string]any{
		"zone":         "sched.test.",
		"scheduled_at": at.Format(time.RFC3339Nano),
		"name":         "create sched.test",
	})
	require.Equal(t, http.StatusAccepted, code, "schedule: %v", body)
	id, _ := body["id"].(string)
	require.NotEmpty(t, id)

	ch, err := stack.Store.Get(context.Background(), id, false)
	require.NoError(t, err)
	require.NotNil(t, ch)
	_, err = stack.Store.MarkRunning(context.Background(), ch.ID, "test", 2*time.Minute)
	require.NoError(t, err)
	ch, err = stack.Store.Get(context.Background(), id, false)
	require.NoError(t, err)

	result := scheduler.ExecuteChange(context.Background(), ch, stack.Store, stack.Client, stack.Cache, scheduler.ExecuteOpts{
		MaxAttempts: 1, Provisioner: stack.Provisioner,
	})
	require.True(t, result.Success, result.Error)
	_, err = stack.Client.QuerySOA(context.Background(), "sched.test.")
	require.NoError(t, err)
}

func catalogPTRTargets(z *dnsx.Zone) []string {
	if z == nil {
		return nil
	}
	var out []string
	for _, rr := range z.AllRRs() {
		ptr, ok := rr.(*dns.PTR)
		if !ok {
			continue
		}
		out = append(out, dnsx.NormalizeZoneName(ptr.Ptr))
	}
	return out
}

func doAPI(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequest(method, path, bytes.NewReader(buf))
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}
