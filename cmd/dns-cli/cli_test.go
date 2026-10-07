package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, opts *cliOptions, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRoot(opts)
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	// Capture package-level fmt prints that go to os.Stdout/Stderr.
	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()

	execErr := root.Execute()

	_ = wOut.Close()
	_ = wErr.Close()
	outBytes, _ := io.ReadAll(rOut)
	errBytes, _ := io.ReadAll(rErr)
	stdout = outBuf.String() + string(outBytes)
	stderr = errBuf.String() + string(errBytes)
	return stdout, stderr, execErr
}

func testServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *cliOptions) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	opts := &cliOptions{
		baseURL: srv.URL,
		apiKey:  "test-key",
	}
	return srv, opts
}

func TestScheduleList(t *testing.T) {
	var gotMethod, gotPath, gotKey string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotKey = r.Method, r.URL.Path, r.Header.Get("X-API-Key")
		if r.URL.Query().Get("status") != "draft" {
			t.Errorf("status=%q", r.URL.Query().Get("status"))
		}
		if r.URL.Query().Get("zone") != "example.com." {
			t.Errorf("zone=%q", r.URL.Query().Get("zone"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"changes": []map[string]any{
				{"id": "c1", "name": "n1", "zone": "example.com.", "status": "draft", "scheduled_at": ""},
			},
			"total": 1,
		})
	})

	stdout, _, err := runCLI(t, opts, "schedule", "list", "--status", "draft", "--zone", "example.com.")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/scheduled-changes" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if gotKey != "test-key" {
		t.Fatalf("api key %q", gotKey)
	}
	if !strings.Contains(stdout, "c1") || !strings.Contains(stdout, "draft") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestScheduleCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "change.json")
	body := map[string]any{
		"name": "from-file",
		"zone": "example.com.",
		"operations": []map[string]any{
			{"action": "add", "name": "www.example.com.", "type": "A", "ttl": 300, "records": []string{"192.0.2.1"}},
		},
	}
	raw, _ := json.Marshal(body)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotMethod, gotPath string
	var gotBody map[string]any
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		defer func() { _ = r.Body.Close() }()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "new-id", "name": "overridden", "zone": "example.com.", "status": "draft",
			"operations": body["operations"],
		})
	})

	stdout, _, err := runCLI(t, opts, "schedule", "create", "--file", path, "--name", "overridden")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/scheduled-changes" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if gotBody["name"] != "overridden" {
		t.Fatalf("body name=%v", gotBody["name"])
	}
	if _, ok := gotBody["operations"]; !ok {
		t.Fatalf("missing operations: %v", gotBody)
	}
	if !strings.Contains(stdout, "new-id") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestScheduleApply(t *testing.T) {
	var gotMethod, gotPath string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "change_id": "abc", "status": "applied", "message": "ok", "new_serial": 42,
		})
	})

	stdout, _, err := runCLI(t, opts, "schedule", "apply", "abc")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/scheduled-changes/abc/apply" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(stdout, "Applied") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestNsupdateDrafts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "upd.txt")
	text := "zone example.com.\nupdate add www 300 A 192.0.2.1\nsend\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotMethod, gotPath, gotCT string
	var gotBody string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotCT = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": []map[string]any{
				{"id": "d1", "name": "draft-1", "zone": "example.com.", "status": "draft"},
			},
			"total": 1,
		})
	})

	stdout, _, err := runCLI(t, opts, "nsupdate", "--drafts", "--file", path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/nsupdate/drafts" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotCT, "text/plain") {
		t.Fatalf("content-type=%q", gotCT)
	}
	if !strings.Contains(gotBody, "update add www") {
		t.Fatalf("body=%q", gotBody)
	}
	if !strings.Contains(stdout, "d1") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestSearch(t *testing.T) {
	var gotMethod, gotPath string
	var gotQuery string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"zone": "example.com.", "serial": 1, "rrsets": []map[string]any{
					{"name": "www.example.com.", "type": "A", "ttl": 300, "records": []string{"192.0.2.1"}},
				}},
			},
			"total_count": 1,
		})
	})

	stdout, _, err := runCLI(t, opts, "search", "--name", "www", "--type", "A")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/search" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotQuery, "name_pattern=www") || !strings.Contains(gotQuery, "type=A") {
		t.Fatalf("query=%q", gotQuery)
	}
	if !strings.Contains(stdout, "www.example.com.") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestSearchZone(t *testing.T) {
	var gotPath string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":     []map[string]any{{"name": "a.example.com.", "type": "A", "ttl": 60, "records": []string{"192.0.2.9"}}},
			"total_count": 1,
		})
	})

	_, _, err := runCLI(t, opts, "search", "--zone", "example.com.", "--name", "a")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/zones/example.com./search" {
		t.Fatalf("path=%q", gotPath)
	}
}

func TestZoneCreate(t *testing.T) {
	var gotMethod, gotPath string
	var body map[string]any
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"zone": "new.example.", "catalog_added": true})
	})
	stdout, _, err := runCLI(t, opts, "zone", "create", "new.example.", "--primary-ns", "ns1.example.", "--no-catalog")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/zones" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if body["zone"] != "new.example." || body["catalog"] != false {
		t.Fatalf("body=%v", body)
	}
	if !strings.Contains(stdout, "Created zone") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestZoneDelete(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"zone": "new.example.", "catalog_removed": true})
	})
	_, _, err := runCLI(t, opts, "zone", "delete", "new.example.", "--keep-files")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/zones/new.example." {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotQuery, "keep_files=true") {
		t.Fatalf("query=%q", gotQuery)
	}
}

func TestZoneCatalogAdd(t *testing.T) {
	var gotMethod, gotPath string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"zone": "staged.example.", "catalog_added": true})
	})
	stdout, _, err := runCLI(t, opts, "zone", "catalog", "add", "staged.example.")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/v1/zones/staged.example./catalog" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(stdout, "Published zone") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestZoneCatalogRemove(t *testing.T) {
	var gotMethod, gotPath string
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"zone": "staged.example.", "catalog_removed": true})
	})
	stdout, _, err := runCLI(t, opts, "zone", "catalog", "remove", "staged.example.")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/zones/staged.example./catalog" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(stdout, "Removed zone") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestRNDCStatusCLI(t *testing.T) {
	_, opts := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rndc/status" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "host": "bind", "port": 953, "connected": true, "seed_mode": "shared_dir", "catalog_enabled": true})
	})
	stdout, _, err := runCLI(t, opts, "rndc", "status")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout, "RNDC: enabled") {
		t.Fatalf("stdout=%q", stdout)
	}
}
