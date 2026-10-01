package httpapi

import (
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/davidgroves/dns-zone-manager-go/internal/ui"
)

type spaFallback struct {
	api      http.Handler
	files    http.Handler
	index    []byte
	hasIndex bool
}

func newSPAFallback(api http.Handler, uiDir string) http.Handler {
	var sub fs.FS
	if uiDir != "" {
		sub = os.DirFS(uiDir)
	} else {
		var err error
		sub, err = fs.Sub(ui.Dist, "dist")
		if err != nil {
			return api
		}
	}
	index, err := fs.ReadFile(sub, "index.html")
	sf := &spaFallback{
		api:      api,
		files:    http.FileServer(http.FS(sub)),
		index:    index,
		hasIndex: err == nil,
	}
	return sf
}

func (s *spaFallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// Browsers opening the site get the SPA. Clients that do not ask for
	// HTML (curl, probes) still receive the API info document at /.
	if (path == "/" || path == "") && s.hasIndex && prefersHTML(r) {
		writeIndex(w, r, s.index)
		return
	}
	// API / docs / health always go to the API mux.
	if isAPIPath(path) {
		s.api.ServeHTTP(w, r)
		return
	}
	// Static asset with extension — try embedded FS.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if strings.Contains(strings.TrimPrefix(path, "/"), ".") {
			s.files.ServeHTTP(w, r)
			return
		}
		// SPA client-side route fallback.
		if s.hasIndex {
			writeIndex(w, r, s.index)
			return
		}
	}
	s.api.ServeHTTP(w, r)
}

func prefersHTML(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func writeIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(index)
	}
}

func isAPIPath(path string) bool {
	if path == "/" || path == "" {
		return true
	}
	prefixes := []string{
		"/v1/", "/health", "/ready", "/metrics", "/docs", "/openapi",
		"/ui/config", "/ui/logo", "/schemas",
	}
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p) || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	// Exact matches without trailing slash variants.
	switch path {
	case "/health", "/ready", "/metrics", "/docs", "/openapi", "/openapi.json", "/openapi.yaml",
		"/ui/config", "/ui/logo":
		return true
	}
	return strings.HasPrefix(path, "/v1")
}
