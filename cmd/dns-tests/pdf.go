package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

const (
	pdfMargin = 14.0
	pdfWidth  = 210.0 - 2*pdfMargin
)

// writePDF renders a short report: a summary table, then any failures.
func writePDF(path string, rep Report) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(pdfMargin, 18, pdfMargin)
	pdf.SetAutoPageBreak(true, 16)
	pdf.SetTitle("DNS Zone Manager test report", false)
	pdf.SetHeaderFuncMode(func() {
		pdf.SetFillColor(18, 42, 69)
		pdf.Rect(0, 0, 210, 12, "F")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetXY(pdfMargin, 3.2)
		pdf.Cell(120, 6, "DNS Zone Manager")
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(pdfWidth-120, 6, "Test report", "", 0, "R", false, 0, "")
	}, true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(110, 118, 128)
		pdf.CellFormat(pdfWidth/2, 6, rep.Started.Local().Format("2 Jan 2006 15:04 MST"), "", 0, "L", false, 0, "")
		pdf.CellFormat(pdfWidth/2, 6, fmt.Sprintf("Page %d", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(pdfWidth, 8, "Test results", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(80, 88, 98)
	meta := rep.Finished.Sub(rep.Started).Round(time.Millisecond).String()
	if rep.Git != "" {
		meta = rep.Git + "   ·   " + meta
	}
	pdf.CellFormat(pdfWidth, 5, pdfSafe(meta), "", 1, "L", false, 0, "")
	pdf.Ln(3)

	overall := "Passed"
	fillR, fillG, fillB := 226, 244, 232
	textR, textG, textB := 20, 110, 60
	if rep.failed() {
		overall = "Failed"
		fillR, fillG, fillB = 252, 232, 230
		textR, textG, textB = 160, 36, 36
	}
	pdf.SetFillColor(fillR, fillG, fillB)
	pdf.SetTextColor(textR, textG, textB)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(pdfWidth, 8, "  "+overall, "", 1, "L", true, 0, "")
	pdf.Ln(4)

	drawSummaryTable(pdf, rep.Suites)
	pdf.Ln(6)

	anyFail := false
	for _, s := range rep.Suites {
		if len(s.Failures) > 0 || (s.Status == statusFailed && s.Detail != "") {
			anyFail = true
			break
		}
	}
	pdf.SetTextColor(18, 42, 69)
	pdf.SetFont("Helvetica", "B", 12)
	if !anyFail {
		pdf.CellFormat(pdfWidth, 7, "No failures", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(80, 88, 98)
		pdf.MultiCell(pdfWidth, 4.5, "Every suite that ran finished without a failing test.", "", "L", false)
	} else {
		pdf.CellFormat(pdfWidth, 7, "Failures", "", 1, "L", false, 0, "")
		for _, s := range rep.Suites {
			if s.Status != statusFailed {
				continue
			}
			pdf.Ln(1)
			pdf.SetFont("Helvetica", "B", 10)
			pdf.SetTextColor(160, 36, 36)
			pdf.CellFormat(pdfWidth, 6, pdfSafe(s.Name), "", 1, "L", false, 0, "")
			if len(s.Failures) == 0 && s.Detail != "" {
				pdf.SetFont("Helvetica", "", 8)
				pdf.SetTextColor(50, 56, 64)
				pdf.MultiCell(pdfWidth, 4, pdfSafe(s.Detail), "", "L", false)
				continue
			}
			for _, f := range s.Failures {
				pdf.SetFont("Helvetica", "B", 8)
				pdf.SetTextColor(30, 36, 44)
				pdf.MultiCell(pdfWidth, 4, pdfSafe(f.Name), "", "L", false)
				if out := truncate(f.Output, 900); out != "" {
					pdf.SetFont("Helvetica", "", 8)
					pdf.SetTextColor(70, 76, 84)
					pdf.MultiCell(pdfWidth, 3.6, pdfSafe(out), "", "L", false)
				}
				pdf.Ln(1)
			}
		}
	}

	return pdf.OutputFileAndClose(path)
}

func drawSummaryTable(pdf *fpdf.Fpdf, suites []SuiteResult) {
	headers := []string{"Suite", "Result", "Passed", "Failed", "Skipped", "Time"}
	widths := []float64{62, 28, 22, 22, 24, 18}
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(18, 42, 69)
	pdf.SetTextColor(255, 255, 255)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, "  "+h, "", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 8)
	for i, s := range suites {
		if i%2 == 0 {
			pdf.SetFillColor(244, 246, 248)
		} else {
			pdf.SetFillColor(255, 255, 255)
		}
		pdf.SetTextColor(30, 36, 44)
		vals := []string{
			s.Name,
			s.Status,
			fmt.Sprintf("%d", s.Passed),
			fmt.Sprintf("%d", s.Failed),
			fmt.Sprintf("%d", s.Skipped),
			s.Duration.Round(time.Millisecond).String(),
		}
		for col, v := range vals {
			if col == 1 {
				switch s.Status {
				case statusPassed:
					pdf.SetTextColor(20, 110, 60)
				case statusFailed:
					pdf.SetTextColor(160, 36, 36)
				default:
					pdf.SetTextColor(140, 98, 20)
				}
			} else {
				pdf.SetTextColor(30, 36, 44)
			}
			pdf.CellFormat(widths[col], 6.5, "  "+pdfSafe(v), "", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
		if s.Detail != "" && s.Status == statusSkipped {
			pdf.SetFont("Helvetica", "I", 7)
			pdf.SetTextColor(110, 118, 128)
			pdf.CellFormat(pdfWidth, 4.5, "  "+pdfSafe(s.Detail), "", 1, "L", false, 0, "")
			pdf.SetFont("Helvetica", "", 8)
		}
	}
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

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
