package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestConsumeGoJSON(t *testing.T) {
	raw := strings.Join([]string{
		`{"Action":"run","Package":"example.com/pkg","Test":"TestOK"}`,
		`{"Action":"pass","Package":"example.com/pkg","Test":"TestOK","Elapsed":0.01}`,
		`{"Action":"run","Package":"example.com/pkg","Test":"TestBad"}`,
		`{"Action":"output","Package":"example.com/pkg","Test":"TestBad","Output":"boom\n"}`,
		`{"Action":"fail","Package":"example.com/pkg","Test":"TestBad","Elapsed":0.02}`,
		`{"Action":"pass","Package":"example.com/pkg","Elapsed":0.05}`,
	}, "\n")
	var buf strings.Builder
	suite := consumeGoJSON(strings.NewReader(raw), &buf, false)
	if suite.Passed != 1 || suite.Failed != 1 {
		t.Fatalf("counts passed=%d failed=%d", suite.Passed, suite.Failed)
	}
	if suite.Status != statusFailed {
		t.Fatalf("status %s", suite.Status)
	}
	if len(suite.Failures) != 1 || !strings.Contains(suite.Failures[0].Output, "boom") {
		t.Fatalf("failures %#v", suite.Failures)
	}
	if !strings.Contains(buf.String(), "FAIL") {
		t.Fatalf("log %q", buf.String())
	}
	if suite.Duration != 50*time.Millisecond {
		t.Fatalf("duration %s", suite.Duration)
	}
}

func TestParseVitest(t *testing.T) {
	raw := []byte(`{
		"numPassedTests": 2,
		"numFailedTests": 1,
		"numPendingTests": 0,
		"success": false,
		"testResults": [{
			"name": "client.test.ts",
			"assertionResults": [
				{"fullName": "client works", "status": "passed", "failureMessages": []},
				{"fullName": "client fails", "status": "failed", "failureMessages": ["expected 1"]}
			]
		}]
	}`)
	suite := parseVitest(raw)
	if suite.Passed != 2 || suite.Failed != 1 || suite.Status != statusFailed {
		t.Fatalf("%+v", suite)
	}
	if len(suite.Failures) != 1 || suite.Failures[0].Name != "client fails" {
		t.Fatalf("failures %#v", suite.Failures)
	}
}

func TestParsePlaywright(t *testing.T) {
	raw := []byte(`{
		"stats": {"expected": 3, "unexpected": 1, "skipped": 0, "flaky": 0, "duration": 1500},
		"errors": [],
		"suites": [{
			"title": "zones.spec.ts",
			"specs": [{
				"title": "lists zones",
				"ok": false,
				"tests": [{"results": [{"status": "failed", "error": {"message": "timeout"}}]}]
			}]
		}]
	}`)
	suite := parsePlaywright(raw)
	if suite.Passed != 3 || suite.Failed != 1 || suite.Status != statusFailed {
		t.Fatalf("%+v", suite)
	}
	if suite.Duration != 1500*time.Millisecond {
		t.Fatalf("duration %s", suite.Duration)
	}
	if len(suite.Failures) != 1 || !strings.Contains(suite.Failures[0].Output, "timeout") {
		t.Fatalf("failures %#v", suite.Failures)
	}
}

func TestWritePDF(t *testing.T) {
	path := t.TempDir() + "/report.pdf"
	rep := Report{
		Started:  time.Now().Add(-2 * time.Second),
		Finished: time.Now(),
		Git:      "v0.test",
		Suites: []SuiteResult{
			{Name: "Go unit", Status: statusPassed, Passed: 10, Duration: 1200 * time.Millisecond},
			{Name: "Frontend unit", Status: statusFailed, Passed: 4, Failed: 1, Duration: 800 * time.Millisecond,
				Failures: []Failure{{Name: "client fails", Output: "expected 1\ngot 2"}}},
			{Name: "Frontend E2E", Status: statusSkipped, Detail: "API not running on http://localhost:8000"},
		},
	}
	if err := writePDF(path, rep); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "%PDF-") {
		t.Fatalf("not a pdf: %q", data[:min(20, len(data))])
	}
	if len(data) < 800 {
		t.Fatalf("pdf too small: %d", len(data))
	}
}
