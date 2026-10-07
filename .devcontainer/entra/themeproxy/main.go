// Theme proxy for the Entra emulator authorize/sign-in HTML.
//
// entra-emulator bakes a light Fluent-style page shell into the binary with
// no theme flag. This reverse proxy listens on the public issuer port (:8444),
// forwards to the emulator on an internal port, and rewrites text/html
// responses to a dark palette matching the SPA / oauth2-proxy sign-in pages.
package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var styleRe = regexp.MustCompile(`(?s)<style>.*?</style>`)

// Dark styles aligned with frontend/styles/app.css (data-theme=dark) and
// .devcontainer/oauth2-proxy templates. Selectors match entra-emulator's
// pageShell in internal/identity/signin.go.
const darkStyle = `<meta name="color-scheme" content="dark">
<style>
body{margin:0;font-family:"Segoe UI",system-ui,sans-serif;background:#0f1419;color:#e6edf3;
display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{background:#1a1f2e;border:1px solid #3d4556;border-radius:10px;
box-shadow:0 8px 24px rgba(0,0,0,.4);
width:440px;max-width:calc(100vw - 32px);padding:44px}
.badge{display:inline-block;background:#d29922;color:#0b1220;font-size:11px;font-weight:600;
letter-spacing:.06em;border-radius:4px;padding:3px 8px;margin-bottom:16px}
h1{font-size:28px;font-weight:600;margin:0 0 16px;color:#e6edf3}
.note{color:#6e7681;font-size:12px;margin-top:24px}
.error{background:rgba(248,81,73,.18);color:#e6edf3;border:1px solid rgba(248,81,73,.45);
border-radius:6px;padding:12px 16px;margin-bottom:16px;font-size:14px}
ul.picker{list-style:none;margin:0;padding:0}
ul.picker li{margin:0}
ul.picker button{display:flex;flex-direction:column;width:100%;text-align:left;background:none;
border:none;border-radius:8px;padding:10px 12px;cursor:pointer;font:inherit;color:#e6edf3}
ul.picker button:hover{background:#2d3548}
.upn{color:#8b949e;font-size:12px}
label{display:block;font-size:14px;font-weight:600;margin:12px 0 4px;color:#8b949e}
input[type=text],input[type=password]{width:100%;box-sizing:border-box;height:36px;border:1px solid #3d4556;
border-radius:6px;padding:6px 10px;font:inherit;background:#252b3b;color:#e6edf3}
input[type=text]:focus,input[type=password]:focus{outline:2px solid rgba(88,166,255,.45);border-color:#58a6ff}
.primary{background:#58a6ff;color:#0b1220;border:none;border-radius:6px;height:36px;padding:8px 20px;
font-size:14px;font-weight:600;cursor:pointer;margin-top:16px;line-height:1}
.primary:hover{background:#4b8fdb}
.scopes{margin:8px 0 0;padding-left:20px;font-size:14px;color:#8b949e}
p{color:#8b949e}
strong{color:#e6edf3}
a{color:#58a6ff}
</style>`

func main() {
	listen := envOr("LISTEN", ":8444")
	upstreamRaw := envOr("UPSTREAM", "http://127.0.0.1:18445")
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		log.Fatalf("UPSTREAM: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = upstream.Host
		// Avoid gzip so ModifyResponse can rewrite HTML as plain text.
		req.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = rewriteHTML
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("upstream error: %v", err)
		http.Error(w, "entra emulator unavailable", http.StatusBadGateway)
	}

	log.Printf("entra theme proxy listening on %s → %s", listen, upstreamRaw)
	log.Fatal(http.ListenAndServe(listen, proxy))
}

func rewriteHTML(resp *http.Response) error {
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if styleRe.Match(body) {
		body = styleRe.ReplaceAll(body, []byte(darkStyle))
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("Content-Encoding")
	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
