package perfharness

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/go-pdf/fpdf"
)

// WriteComparePDF writes a manager-facing one-zone vs many-zone briefing.
func WriteComparePDF(path string, one, many map[string]any) error {
	b, err := renderComparePDF(one, many)
	if err != nil {
		return err
	}
	if err := mkdirParent(path); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func renderComparePDF(one, many map[string]any) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.SetMargins(pdfMargin, 18, pdfMargin)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetTitle("Single-zone vs many-zone API writes", false)
	pdf.SetHeaderFuncMode(func() {
		pdf.SetFillColor(18, 42, 69)
		pdf.Rect(0, 0, 210, 12, "F")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetXY(pdfMargin, 3.2)
		pdf.Cell(120, 6, "DNS Zone Manager")
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(pdfWidth-120, 6, "Zone scaling briefing", "", 0, "R", false, 0, "")
	}, true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 8)
		setRGB(pdf, colorMuted, false)
		when := time.Now().Local().Format("2 Jan 2006 15:04 MST")
		pdf.CellFormat(pdfWidth/2, 6, pdfSafe(when), "", 0, "L", false, 0, "")
		pdf.CellFormat(pdfWidth/2, 6, fmt.Sprintf("Page %d", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	oneSteps := stepsByLabel(one["steps"])
	manySteps := stepsByLabel(many["steps"])
	oneHeld := maxHeldRate(oneSteps)
	manyHeld := maxHeldRate(manySteps)
	_, onePeak, oneP99, oneErr := summarizeSteps(oneSteps)
	_, manyPeak, manyP99, manyErr := summarizeSteps(manySteps)

	setRGB(pdf, colorNavy, false)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(pdfWidth, 8, "Single-zone vs many-zone API writes", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	setRGB(pdf, colorSlate, false)
	pdf.MultiCell(pdfWidth, 4.5, pdfSafe(fmt.Sprintf("%s  vs  %s", fmt.Sprint(one["scenario"]), fmt.Sprint(many["scenario"]))), "", "L", false)
	pdf.Ln(2)

	switch {
	case manyHeld > oneHeld+50:
		setRGB(pdf, colorOKFill, true)
		setRGB(pdf, colorOKText, false)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(pdfWidth, 8, "  Spreading updates across zones holds more load than one zone", "", 1, "L", true, 0, "")
	case oneHeld > manyHeld+50:
		setRGB(pdf, colorBadFill, true)
		setRGB(pdf, colorDanger, false)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(pdfWidth, 8, "  Single-zone held more load than the many-zone run", "", 1, "L", true, 0, "")
	default:
		setRGB(pdf, colorCardBG, true)
		setRGB(pdf, colorNavy, false)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(pdfWidth, 8, "  Both runs cliff at the same offered rate", "", 1, "L", true, 0, "")
	}
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "", 9)
	setRGB(pdf, colorInk, false)
	pdf.MultiCell(pdfWidth, 4.5, pdfSafe(
		"BIND applies dynamic updates per zone (journal + SOA serial). A hot zone serializes those writes. "+
			"The API can issue updates concurrently; when they all hit one zone, BIND is the ceiling. "+
			"Round-robin across many small zones lets BIND work those journals in parallel.",
	), "", "L", false)
	pdf.Ln(4)

	cards := []struct{ label, value, hint string }{
		{"One-zone held", fmt.Sprintf("%.0f /s", oneHeld), "last target with <5% errors"},
		{"Many-zone held", fmt.Sprintf("%.0f /s", manyHeld), "same criterion"},
		{"One-zone peak", fmt.Sprintf("%.0f /s", onePeak), fmt.Sprintf("p99 %.0f ms, errors %.0f%%", oneP99, oneErr*100)},
		{"Many-zone peak", fmt.Sprintf("%.0f /s", manyPeak), fmt.Sprintf("p99 %.0f ms, errors %.0f%%", manyP99, manyErr*100)},
	}
	gap := 3.0
	w := (pdfWidth - gap*3) / 4
	y := pdf.GetY()
	for i, card := range cards {
		x := pdfMargin + float64(i)*(w+gap)
		setRGB(pdf, colorCardBG, true)
		pdf.RoundedRect(x, y, w, 24, 2, "1234", "F")
		pdf.SetXY(x+3, y+2.5)
		pdf.SetFont("Helvetica", "", 7)
		setRGB(pdf, colorMuted, false)
		pdf.CellFormat(w-6, 4, card.label, "", 1, "L", false, 0, "")
		pdf.SetX(x + 3)
		pdf.SetFont("Helvetica", "B", 13)
		setRGB(pdf, colorNavy, false)
		pdf.CellFormat(w-6, 7, pdfSafe(card.value), "", 1, "L", false, 0, "")
		pdf.SetX(x + 3)
		pdf.SetFont("Helvetica", "", 6.5)
		setRGB(pdf, colorSlate, false)
		pdf.CellFormat(w-6, 4, pdfSafe(card.hint), "", 0, "L", false, 0, "")
	}
	pdf.SetY(y + 26)
	pdf.Ln(2)

	targets := sortedTargets(oneSteps, manySteps)
	oneBy := stepByTarget(oneSteps)
	manyBy := stepByTarget(manySteps)
	oneAch := make([]float64, len(targets))
	manyAch := make([]float64, len(targets))
	oneLat := make([]float64, len(targets))
	manyLat := make([]float64, len(targets))
	for i, t := range targets {
		if s := oneBy[t]; s != nil {
			oneAch[i], _ = asFloat(s["achieved_rps"])
			oneLat[i], _ = asFloat(asMap(s["writers"])["p99_ms"])
		}
		if s := manyBy[t]; s != nil {
			manyAch[i], _ = asFloat(s["achieved_rps"])
			manyLat[i], _ = asFloat(asMap(s["writers"])["p99_ms"])
		}
	}

	ensureSpace(pdf, 78)
	drawLineChart(pdf, "Achieved throughput vs offered rate", "offered rate (req/s)", targets, []struct {
		Name  string
		Color [3]int
		Ys    []float64
	}{
		{"One zone", colorOne, oneAch},
		{"Many zones", colorMany, manyAch},
	})
	pdf.Ln(4)
	ensureSpace(pdf, 78)
	drawLineChart(pdf, "p99 latency vs offered rate (ms)", "offered rate (req/s)", targets, []struct {
		Name  string
		Color [3]int
		Ys    []float64
	}{
		{"One zone p99", colorOne, oneLat},
		{"Many zones p99", colorMany, manyLat},
	})
	pdf.Ln(4)

	ensureSpace(pdf, 20+float64(len(targets))*6.5)
	sectionTitle(pdf, "Per-step comparison")
	headers := []string{"Target", "One rps", "Many rps", "One p99", "Many p99", "One err%", "Many err%"}
	widths := []float64{24, 24, 26, 24, 26, 24, 34}
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(colorNavy[0], colorNavy[1], colorNavy[2])
	pdf.SetTextColor(255, 255, 255)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, "  "+h, "", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 8)
	for i, t := range targets {
		if i%2 == 0 {
			pdf.SetFillColor(244, 246, 248)
		} else {
			pdf.SetFillColor(255, 255, 255)
		}
		oneW, manyW := asMap(nil), asMap(nil)
		if s := oneBy[t]; s != nil {
			oneW = asMap(s["writers"])
		}
		if s := manyBy[t]; s != nil {
			manyW = asMap(s["writers"])
		}
		oneCnt, _ := asFloat(oneW["count"])
		oneE, _ := asFloat(oneW["errors"])
		manyCnt, _ := asFloat(manyW["count"])
		manyE, _ := asFloat(manyW["errors"])
		onePct, manyPct := 0.0, 0.0
		if oneCnt > 0 {
			onePct = 100 * oneE / oneCnt
		}
		if manyCnt > 0 {
			manyPct = 100 * manyE / manyCnt
		}
		vals := []string{
			fmt.Sprintf("%.0f", t),
			fmt.Sprintf("%.0f", oneAch[i]),
			fmt.Sprintf("%.0f", manyAch[i]),
			fmt.Sprintf("%.1f", oneLat[i]),
			fmt.Sprintf("%.1f", manyLat[i]),
			fmt.Sprintf("%.1f", onePct),
			fmt.Sprintf("%.1f", manyPct),
		}
		for col, v := range vals {
			setRGB(pdf, colorInk, false)
			if (col == 5 && onePct >= 5) || (col == 6 && manyPct >= 5) {
				setRGB(pdf, colorDanger, false)
			}
			pdf.CellFormat(widths[col], 6.5, "  "+v, "", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.Ln(4)
	drawList(pdf, "How to read this", []string{
		"One-zone: every update hits the same BIND journal/SOA.",
		"Many-zone: the same API, client, and DDNS pool, round-robin across catalog zones.",
		"If many-zone holds a higher rate, BIND is serializing that one hot zone.",
		"If both cliff together, the limit is shared (BIND UPDATE path or dns.pool_size), not one zone's journal.",
	}, colorNavy[0], colorNavy[1], colorNavy[2])

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
