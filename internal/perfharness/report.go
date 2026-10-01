package perfharness

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Report is one scenario run.
type Report struct {
	Scenario     string             `json:"scenario"`
	StartedAt    string             `json:"started_at"`
	FinishedAt   string             `json:"finished_at"`
	Host         map[string]any     `json:"host"`
	Config       map[string]any     `json:"config"`
	Steps        []StepResult       `json:"steps"`
	Provision    map[string]any     `json:"provision"`
	Probes       map[string]any     `json:"probes"`
	MetricsDelta map[string]float64 `json:"metrics_delta"`
	Notes        []string           `json:"notes"`
	Errors       []string           `json:"errors"`
}

// ModuleRoot is the repository root that contains perf/scenarios.
func ModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "perf", "scenarios")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

func ScenariosDir() string { return filepath.Join(ModuleRoot(), "perf", "scenarios") }

func ResultsDir() string { return filepath.Join(ModuleRoot(), "perf", "results") }

// Written is the set of files produced for one run.
type Written struct {
	JSON string
	MD   string
	PDF  string
}

// WriteReport writes JSON, Markdown, and PDF, and refreshes the latest.* copies.
func WriteReport(rep Report) (Written, error) {
	dir := ResultsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Written{}, err
	}
	slug := time.Now().UTC().Format("20060102T150405Z") + "-" + slugPart(rep.Scenario) + "-" + uuid.NewString()[:8]
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return Written{}, err
	}
	data = append(data, '\n')
	asMap := map[string]any{}
	if err := json.Unmarshal(data, &asMap); err != nil {
		return Written{}, err
	}
	md := RenderMarkdown(asMap)
	pdfBytes, err := renderPDF(asMap)
	if err != nil {
		return Written{}, err
	}
	out := Written{
		JSON: filepath.Join(dir, slug+".json"),
		MD:   filepath.Join(dir, slug+".md"),
		PDF:  filepath.Join(dir, slug+".pdf"),
	}
	if err := os.WriteFile(out.JSON, data, 0o644); err != nil {
		return Written{}, err
	}
	if err := os.WriteFile(out.MD, []byte(md), 0o644); err != nil {
		return Written{}, err
	}
	if err := os.WriteFile(out.PDF, pdfBytes, 0o644); err != nil {
		return Written{}, err
	}
	_ = os.WriteFile(filepath.Join(dir, "latest.json"), data, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "latest.md"), []byte(md), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "latest.pdf"), pdfBytes, 0o644)
	return out, nil
}

func slugPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}

// LoadReport reads a JSON report into a generic map so older Python reports still render.
func LoadReport(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// RenderMarkdown formats a report map the same way the previous harness did.
func RenderMarkdown(data map[string]any) string {
	var lines []string
	scenario, _ := data["scenario"].(string)
	if scenario == "" {
		scenario = "unknown"
	}
	lines = append(lines, "# Perf: "+scenario, "")
	lines = append(lines, fmt.Sprintf("- Started: `%v`", data["started_at"]))
	lines = append(lines, fmt.Sprintf("- Finished: `%v`", data["finished_at"]))
	if cfg, ok := data["config"].(map[string]any); ok && len(cfg) > 0 {
		raw, _ := json.Marshal(cfg)
		lines = append(lines, "- Config: `"+string(raw)+"`")
	}
	lines = append(lines, "")

	if prov, ok := data["provision"].(map[string]any); ok && len(prov) > 0 {
		lines = append(lines, "## Provision", "", "| Metric | Value |", "| --- | --- |")
		for _, key := range []string{
			"zone", "records", "generate_seconds", "generate_bytes", "bind_add_seconds",
			"bind_ready_seconds", "axfr_refresh_seconds", "cache_size_bytes_before",
			"cache_size_bytes_after", "soa_serial",
		} {
			if prov[key] != nil {
				lines = append(lines, fmt.Sprintf("| %s | %v |", key, prov[key]))
			}
		}
		if errs, ok := prov["errors"].([]any); ok && len(errs) > 0 {
			parts := make([]string, len(errs))
			for i, e := range errs {
				parts[i] = fmt.Sprint(e)
			}
			lines = append(lines, "", "Errors: "+strings.Join(parts, "; "))
		}
		lines = append(lines, "")
	}

	steps, _ := data["steps"].([]any)
	if len(steps) > 0 {
		lines = append(lines, "## Load steps", "",
			"| Step | Target rps | Achieved rps | Count | Errors | p50 ms | p95 ms | p99 ms | max ms |",
			"| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
		for _, raw := range steps {
			step, _ := raw.(map[string]any)
			w := asMap(step["writers"])
			lines = append(lines, fmt.Sprintf("| %v | %s | %s | %s | %s | %s | %s | %s | %s |",
				step["label"], fmtRPS(step["target_rps"]), fmtRPS(step["achieved_rps"]),
				fmtCount(w["count"]), fmtCount(w["errors"]),
				fmtMS(w["p50_ms"]), fmtMS(w["p95_ms"]), fmtMS(w["p99_ms"]), fmtMS(w["max_ms"])))
		}
		lines = append(lines, "")
		for _, raw := range steps {
			step, _ := raw.(map[string]any)
			if readers, ok := step["readers"].(map[string]any); ok && len(readers) > 0 {
				lines = append(lines, fmt.Sprintf("### Readers (%v)", step["label"]), "",
					"| Op | Count | Errors | p50 ms | p95 ms | p99 ms |",
					"| --- | ---: | ---: | ---: | ---: | ---: |")
				names := make([]string, 0, len(readers))
				for name := range readers {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, op := range names {
					summary := asMap(readers[op])
					lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s | %s |",
						op, fmtCount(summary["count"]), fmtCount(summary["errors"]),
						fmtMS(summary["p50_ms"]), fmtMS(summary["p95_ms"]), fmtMS(summary["p99_ms"])))
				}
				lines = append(lines, "")
			}
			if ws := asMap(step["websocket"]); len(ws) > 0 {
				raw, _ := json.MarshalIndent(ws, "", "  ")
				lines = append(lines, fmt.Sprintf("### WebSocket (%v)", step["label"]), "", "```json", string(raw), "```", "")
			}
		}
	}

	if delta, ok := data["metrics_delta"].(map[string]any); ok && len(delta) > 0 {
		lines = append(lines, "## Metrics delta", "", "| Metric | Delta |", "| --- | ---: |")
		keys := make([]string, 0, len(delta))
		for k, v := range delta {
			if f, ok := asFloat(v); ok && abs(f) >= 1e-9 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		shown := 0
		for _, k := range keys {
			f, _ := asFloat(delta[k])
			lines = append(lines, fmt.Sprintf("| `%s` | %g |", k, f))
			shown++
			if shown >= 40 {
				lines = append(lines, "| … | (truncated) |")
				break
			}
		}
		lines = append(lines, "")
	}

	if probes, ok := data["probes"].(map[string]any); ok {
		if probes["before"] != nil || probes["after"] != nil {
			lines = append(lines, "## Probes", "")
			for _, label := range []string{"before", "after"} {
				block := asMap(probes[label])
				if len(block) == 0 {
					continue
				}
				lines = append(lines, "### "+label)
				if stats := asMap(block["docker_stats"]); len(stats) > 0 {
					raw, _ := json.MarshalIndent(stats, "", "  ")
					lines = append(lines, "", "Docker stats:", "", "```json", string(raw), "```")
				}
				if lag := block["serial_lag"]; lag != nil {
					lines = append(lines, "", "Serial lag: `"+compactJSON(lag)+"`")
				}
				if serial := block["serial"]; serial != nil {
					lines = append(lines, "", "Serial sample: `"+compactJSON(serial)+"`")
				}
				lines = append(lines, "")
			}
		}
	}

	if notes, ok := data["notes"].([]any); ok && len(notes) > 0 {
		lines = append(lines, "## Notes", "")
		for _, n := range notes {
			lines = append(lines, "- "+fmt.Sprint(n))
		}
		lines = append(lines, "")
	}
	if errs, ok := data["errors"].([]any); ok && len(errs) > 0 {
		lines = append(lines, "## Errors", "")
		for _, e := range errs {
			lines = append(lines, "- "+fmt.Sprint(e))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n"
}

// CompareReports prints achieved rate and p99 for two saved runs.
func CompareReports(a, b map[string]any) string {
	lines := []string{
		fmt.Sprintf("# Compare: %v vs %v", a["scenario"], b["scenario"]),
		"",
		fmt.Sprintf("- A: `%v`", a["started_at"]),
		fmt.Sprintf("- B: `%v`", b["started_at"]),
		"",
		"| Step | A rps | B rps | Δ rps | A p99 | B p99 | Δ p99 |",
		"| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
	}
	stepsA := stepsByLabel(a["steps"])
	stepsB := stepsByLabel(b["steps"])
	seen := map[string]struct{}{}
	var labels []string
	for _, s := range append(append([]map[string]any{}, stepsA...), stepsB...) {
		label := fmt.Sprint(s["label"])
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	byA := map[string]map[string]any{}
	byB := map[string]map[string]any{}
	for _, s := range stepsA {
		byA[fmt.Sprint(s["label"])] = s
	}
	for _, s := range stepsB {
		byB[fmt.Sprint(s["label"])] = s
	}
	for _, label := range labels {
		sa, sb := byA[label], byB[label]
		ra, _ := asFloat(sa["achieved_rps"])
		rb, _ := asFloat(sb["achieved_rps"])
		pa, _ := asFloat(asMap(sa["writers"])["p99_ms"])
		pb, _ := asFloat(asMap(sb["writers"])["p99_ms"])
		lines = append(lines, fmt.Sprintf("| %s | %.1f | %.1f | %+.1f | %.1f | %.1f | %+.1f |",
			label, ra, rb, rb-ra, pa, pb, pb-pa))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func stepsByLabel(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func compactJSON(v any) string {
	b, err := json.Marshal(wholeNumbers(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func wholeNumbers(v any) any {
	switch n := v.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n) && math.Abs(n) < 1e16 {
			return int64(n)
		}
		return n
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			out[k] = wholeNumbers(val)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, val := range n {
			out[i] = wholeNumbers(val)
		}
		return out
	default:
		return v
	}
}

func formatValue(v any) string {
	if f, ok := asFloat(v); ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return strconv.FormatInt(int64(f), 10)
	}
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func fmtMS(v any) string {
	f, ok := asFloat(v)
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.1f", f)
}

func fmtRPS(v any) string {
	f, ok := asFloat(v)
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.1f", f)
}

func fmtCount(v any) string {
	f, ok := asFloat(v)
	if !ok {
		return "0"
	}
	return fmt.Sprintf("%.0f", f)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func hostInfo() map[string]any {
	name, _ := os.Hostname()
	return map[string]any{
		"go":       runtime.Version(),
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"hostname": name,
	}
}

// WritePDFFile renders a report map to path.
func WritePDFFile(path string, data map[string]any) error {
	b, err := renderPDF(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
