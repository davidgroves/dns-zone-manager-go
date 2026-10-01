package perfharness

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func urlPath(zone string) string {
	return url.PathEscape(trimDot(zone))
}

func healthOKNow(cfg Config) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.APIBase, "/")+"/health", nil)
	if err != nil {
		return false, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}
