package perfharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPercentileNearestRank(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(xs, 50); got != 5 {
		t.Fatalf("p50 = %v", got)
	}
	if got := percentile(xs, 99); got != 10 {
		t.Fatalf("p99 = %v", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Fatalf("empty = %v", got)
	}
}

func TestZoneRoundRobin(t *testing.T) {
	targets := TargetZones("alpha.test", []string{" one.test ", "", "two.test"})
	if len(targets) != 2 || targets[0] != "one.test" || targets[1] != "two.test" {
		t.Fatalf("targets = %#v", targets)
	}
	if got := ZoneForSeq(targets, 1); got != "one.test" {
		t.Fatalf("seq 1 = %s", got)
	}
	if got := ZoneForSeq(targets, 2); got != "two.test" {
		t.Fatalf("seq 2 = %s", got)
	}
	if got := ZoneForSeq(targets, 3); got != "one.test" {
		t.Fatalf("seq 3 = %s", got)
	}
	if got := TargetZones("only.test", nil); len(got) != 1 || got[0] != "only.test" {
		t.Fatalf("fallback = %#v", got)
	}
	if writeName(1, 2000) != "perfchg-000002" || writeAddr(1) != "203.0.113.2" {
		t.Fatalf("name/addr %s %s", writeName(1, 2000), writeAddr(1))
	}
}

func TestGenerateZoneShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.example.test")
	gen, err := GenerateZone("example.test", 20, 42, path)
	if err != nil {
		t.Fatal(err)
	}
	if gen.Records != 20 || gen.Bytes <= 0 {
		t.Fatalf("gen = %+v", gen)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"$ORIGIN example.test.", "SOA", "host0000001", "host0000020"} {
		if !strings.Contains(text, want) {
			t.Fatalf("zone file missing %q", want)
		}
	}
}

func TestMetricsDeltaSkipsCreated(t *testing.T) {
	text := `
# comment
dns_zone_manager_ddns_updates_total 3
dns_zone_manager_ddns_updates_created 1.7e9
other_metric 9
dns_zone_manager_cache_size_bytes{zone="a.test."} 10
`
	parsed := ParsePrometheus(text)
	if parsed["dns_zone_manager_ddns_updates_total"] != 3 {
		t.Fatalf("parsed = %#v", parsed)
	}
	if _, ok := parsed["other_metric"]; ok {
		t.Fatalf("kept unrelated metric: %#v", parsed)
	}
	delta := MetricsDelta(
		map[string]float64{"dns_zone_manager_ddns_updates_total": 1, "dns_zone_manager_ddns_updates_created": 5},
		map[string]float64{"dns_zone_manager_ddns_updates_total": 4, "dns_zone_manager_ddns_updates_created": 9},
	)
	if delta["dns_zone_manager_ddns_updates_total"] != 3 {
		t.Fatalf("delta = %#v", delta)
	}
	if _, ok := delta["dns_zone_manager_ddns_updates_created"]; ok {
		t.Fatalf("kept _created: %#v", delta)
	}
}

func TestPDFContainsScenario(t *testing.T) {
	data := map[string]any{
		"scenario":    "writes-one-zone",
		"started_at":  "2026-10-01T13:00:00Z",
		"finished_at": "2026-10-01T13:00:30Z",
		"config": map[string]any{
			"zone": "test.local.", "bind": "bind:15353", "api_base": "http://127.0.0.1:8000",
		},
		"steps": []any{
			map[string]any{
				"label": "one-zone@500", "target_rps": 500.0, "achieved_rps": 250.0,
				"writers": map[string]any{"count": 5000.0, "errors": 2500.0, "p50_ms": 1.0, "p99_ms": 1.7},
			},
		},
		"metrics_delta": map[string]any{"dns_zone_manager_ddns_updates_total": 2500.0},
		"notes":         []any{"half the updates were refused"},
		"errors":        []any{},
		"probes": map[string]any{
			"after": map[string]any{
				"serial":       map[string]any{"bind_serial": 10.0, "app_serial": 10.0},
				"docker_stats": map[string]any{"bind": map[string]any{"cpu_perc": "1%", "mem_usage": "20MiB", "mem_perc": "1%"}},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := WritePDFFile(path, data); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.HasPrefix(text, "%PDF-") {
		t.Fatalf("not a pdf: %q", text[:20])
	}
	for _, want := range []string{"writes-one-zone", "Did not hold the offered rate", "one-zone@500"} {
		if !strings.Contains(text, want) {
			t.Fatalf("pdf missing %q", want)
		}
	}
}

func TestListScenarios(t *testing.T) {
	names, err := ListScenarios()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range names {
		if name == "writes-one-zone" {
			found = true
		}
	}
	if !found {
		t.Fatalf("scenarios = %v", names)
	}
	sc, err := LoadScenario("writes-many-zones")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Load == nil || len(sc.Load.Zones) < 2 || sc.Load.Steps[0].RPS != 500 {
		t.Fatalf("scenario = %+v", sc.Load)
	}
}
