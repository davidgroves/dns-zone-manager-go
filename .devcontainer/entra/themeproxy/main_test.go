package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRewriteHTMLDarkensPageShell(t *testing.T) {
	light := `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Sign in — Entra Emulator</title>
<style>
body{margin:0;background:#faf9f8;color:#201f1e}
.card{background:#fff}
</style></head>
<body><main class="card"><h1>Sign in</h1></main></body></html>`

	rec := httptest.NewRecorder()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(light)),
	}
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")

	if err := rewriteHTML(resp); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = rec
	s := string(out)
	if strings.Contains(s, "#faf9f8") {
		t.Fatalf("light canvas color still present:\n%s", s)
	}
	if !strings.Contains(s, "#0f1419") {
		t.Fatalf("expected dark canvas:\n%s", s)
	}
	if !strings.Contains(s, `color-scheme" content="dark"`) {
		t.Fatalf("expected color-scheme meta:\n%s", s)
	}
	if !strings.Contains(s, "<h1>Sign in</h1>") {
		t.Fatalf("body content lost:\n%s", s)
	}
}

func TestRewriteHTMLSkipsNonHTML(t *testing.T) {
	payload := `{"status":"ok"}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
	resp.Header.Set("Content-Type", "application/json")
	if err := rewriteHTML(resp); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != payload {
		t.Fatalf("json mutated: %q", out)
	}
}
