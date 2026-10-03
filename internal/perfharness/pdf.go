package perfharness

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

const (
	pdfMargin = 14.0
	pdfWidth  = 210.0 - 2*pdfMargin
)

func renderPDF(data map[string]any) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.SetMargins(pdfMargin, 18, pdfMargin)
	pdf.SetAutoPageBreak(true, 16)
	scenario, _ := data["scenario"].(string)
	if scenario == "" {
		scenario = "performance run"
	}
	pdf.SetTitle("Performance: "+scenario, false)
	pdf.SetHeaderFuncMode(func() {
		pdf.SetFillColor(18, 42, 69)
		pdf.Rect(0, 0, 210, 12, "F")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetXY(pdfMargin, 3.2)
		pdf.Cell(120, 6, "DNS Zone Manager")
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(pdfWidth-120, 6, "Performance report", "", 0, "R", false, 0, "")
	}, true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(110, 118, 128)
		when := fmt.Sprint(data["started_at"])
		if t, err := time.Parse(time.RFC3339Nano, when); err == nil {
			when = t.Local().Format("2 Jan 2006 15:04 MST")
		}
		pdf.CellFormat(pdfWidth/2, 6, pdfSafe(when), "", 0, "L", false, 0, "")
		pdf.CellFormat(pdfWidth/2, 6, fmt.Sprintf("Page %d", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(pdfWidth, 8, pdfSafe(scenario), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(80, 88, 98)
	pdf.CellFormat(pdfWidth, 5, pdfSafe(configLine(data)), "", 1, "L", false, 0, "")
	pdf.Ln(3)

	held, banner := verdict(data)
	if held {
		pdf.SetFillColor(226, 244, 232)
		pdf.SetTextColor(20, 110, 60)
	} else {
		pdf.SetFillColor(252, 232, 230)
		pdf.SetTextColor(160, 36, 36)
	}
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(pdfWidth, 8, "  "+pdfSafe(banner), "", 1, "L", true, 0, "")
	pdf.Ln(4)

	drawKPICards(pdf, stepsByLabel(data["steps"]))
	pdf.Ln(4)

	steps := stepsByLabel(data["steps"])
	if len(steps) > 0 {
		ensureSpace(pdf, 78)
		drawGroupedBarChart(pdf, "Throughput: target vs achieved (requests/s)", steps, throughputSeries())
		pdf.Ln(6)
		ensureSpace(pdf, 78)
		drawLatencyScalingChart(pdf, steps)
		pdf.Ln(6)
		ensureSpace(pdf, 55)
		drawAttainmentChart(pdf, steps)
		pdf.Ln(5)
	}

	drawStepTable(pdf, steps)
	pdf.Ln(5)

	if delta, ok := data["metrics_delta"].(map[string]any); ok {
		drawDelta(pdf, delta)
	}
	drawSerial(pdf, data)
	drawDocker(pdf, data)
	drawList(pdf, "Notes", stringList(data["notes"]), 18, 42, 69)
	drawList(pdf, "Errors", stringList(data["errors"]), 160, 36, 36)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func configLine(data map[string]any) string {
	cfg := asMap(data["config"])
	parts := []string{}
	if z := fmt.Sprint(cfg["zone"]); z != "" && z != "<nil>" {
		parts = append(parts, z)
	}
	if b := fmt.Sprint(cfg["bind"]); b != "" && b != "<nil>" {
		parts = append(parts, "BIND "+b)
	}
	if a := fmt.Sprint(cfg["api_base"]); a != "" && a != "<nil>" {
		parts = append(parts, a)
	}
	started := fmt.Sprint(data["started_at"])
	finished := fmt.Sprint(data["finished_at"])
	if started != "" && started != "<nil>" {
		parts = append(parts, started)
		if finished != "" && finished != "<nil>" && finished != started {
			parts = append(parts, "to "+finished)
		}
	}
	return strings.Join(parts, "   ")
}

func verdict(data map[string]any) (bool, string) {
	if errs := stringList(data["errors"]); len(errs) > 0 {
		return false, "Completed with errors"
	}
	steps := stepsByLabel(data["steps"])
	if len(steps) == 0 {
		return true, "Completed"
	}
	for _, step := range steps {
		target, _ := asFloat(step["target_rps"])
		if target <= 0 {
			continue
		}
		achieved, _ := asFloat(step["achieved_rps"])
		w := asMap(step["writers"])
		count, _ := asFloat(w["count"])
		errs, _ := asFloat(w["errors"])
		if achieved < 0.9*target || (count > 0 && errs/count >= 0.05) {
			return false, "Did not hold the offered rate"
		}
	}
	return true, "Held the offered rate"
}

func drawStepTable(pdf *fpdf.Fpdf, steps []map[string]any) {
	if len(steps) == 0 {
		return
	}
	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(pdfWidth, 7, "Load steps", "", 1, "L", false, 0, "")
	headers := []string{"Step", "Target", "Achieved", "Count", "Errors", "p50", "p99"}
	widths := []float64{42, 22, 24, 22, 22, 22, 28}
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(18, 42, 69)
	pdf.SetTextColor(255, 255, 255)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, "  "+h, "", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 8)
	for i, step := range steps {
		if i%2 == 0 {
			pdf.SetFillColor(244, 246, 248)
		} else {
			pdf.SetFillColor(255, 255, 255)
		}
		w := asMap(step["writers"])
		errs, _ := asFloat(w["errors"])
		vals := []string{
			fmt.Sprint(step["label"]),
			fmtRPS(step["target_rps"]),
			fmtRPS(step["achieved_rps"]),
			fmtCount(w["count"]),
			fmtCount(w["errors"]),
			fmtMS(w["p50_ms"]),
			fmtMS(w["p99_ms"]),
		}
		for col, v := range vals {
			if col == 4 && errs > 0 {
				pdf.SetTextColor(160, 36, 36)
			} else {
				pdf.SetTextColor(30, 36, 44)
			}
			pdf.CellFormat(widths[col], 6.5, "  "+pdfSafe(fit(v, widths[col])), "", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
	}
}

func drawDelta(pdf *fpdf.Fpdf, delta map[string]any) {
	type pair struct {
		k string
		v float64
	}
	var rows []pair
	for k, v := range delta {
		f, ok := asFloat(v)
		if !ok || abs(f) < 1e-9 {
			continue
		}
		rows = append(rows, pair{k, f})
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].k < rows[j].k })
	if len(rows) > 15 {
		rows = rows[:15]
	}
	pdf.Ln(2)
	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(pdfWidth, 7, "Metrics delta", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(50, 56, 64)
	for _, row := range rows {
		pdf.CellFormat(pdfWidth*0.72, 5, pdfSafe(row.k), "", 0, "L", false, 0, "")
		pdf.CellFormat(pdfWidth*0.28, 5, fmt.Sprintf("%g", row.v), "", 1, "R", false, 0, "")
	}
}

func drawSerial(pdf *fpdf.Fpdf, data map[string]any) {
	probes := asMap(data["probes"])
	after := asMap(probes["after"])
	serial := asMap(after["serial"])
	if len(serial) == 0 {
		serial = asMap(after["serial_lag"])
	}
	if len(serial) == 0 {
		return
	}
	pdf.Ln(2)
	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(pdfWidth, 7, "SOA serial", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(50, 56, 64)
	pdf.CellFormat(pdfWidth, 5, pdfSafe(fmt.Sprintf("BIND %s    API %s", formatValue(serial["bind_serial"]), formatValue(serial["app_serial"]))), "", 1, "L", false, 0, "")
}

func drawDocker(pdf *fpdf.Fpdf, data map[string]any) {
	probes := asMap(data["probes"])
	after := asMap(probes["after"])
	stats := asMap(after["docker_stats"])
	if len(stats) == 0 {
		return
	}
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	pdf.Ln(2)
	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(pdfWidth, 7, "Containers", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(50, 56, 64)
	for _, name := range names {
		st := asMap(stats[name])
		line := fmt.Sprintf("%s    CPU %v    mem %v (%v)", name, st["cpu_perc"], st["mem_usage"], st["mem_perc"])
		pdf.CellFormat(pdfWidth, 5, pdfSafe(line), "", 1, "L", false, 0, "")
	}
}

func drawList(pdf *fpdf.Fpdf, title string, items []string, r, g, b int) {
	if len(items) == 0 {
		return
	}
	pdf.Ln(2)
	pdf.SetTextColor(r, g, b)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(pdfWidth, 7, title, "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(50, 56, 64)
	for _, item := range items {
		pdf.MultiCell(pdfWidth, 4.5, pdfSafe("- "+item), "", "L", false)
	}
}

func stringList(v any) []string {
	switch items := v.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return items
	default:
		return nil
	}
}

func fit(s string, width float64) string {
	max := int(width / 1.7)
	if max < 8 {
		max = 8
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "."
}

func pdfSafe(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\t':
			b.WriteString("  ")
		case '\r':
			continue
		default:
			if r == '\n' || (r >= 32 && r <= 255) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
