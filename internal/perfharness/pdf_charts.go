package perfharness

import (
	"fmt"
	"math"
	"sort"

	"github.com/go-pdf/fpdf"
)

var (
	colorNavy     = [3]int{18, 42, 69}
	colorSlate    = [3]int{80, 88, 98}
	colorInk      = [3]int{30, 36, 44}
	colorMuted    = [3]int{110, 118, 128}
	colorTarget   = [3]int{148, 163, 184}
	colorAchieved = [3]int{37, 99, 235}
	colorP50      = [3]int{16, 185, 129}
	colorP99      = [3]int{245, 158, 11}
	colorDanger   = [3]int{160, 36, 36}
	colorOKFill   = [3]int{226, 244, 232}
	colorOKText   = [3]int{20, 110, 60}
	colorBadFill  = [3]int{252, 232, 230}
	colorCardBG   = [3]int{244, 246, 248}
	colorGrid     = [3]int{226, 232, 240}
	colorMany     = [3]int{16, 185, 129}
	colorOne      = [3]int{37, 99, 235}
)

type chartSeries struct {
	Name  string
	Color [3]int
	Value func(step map[string]any) float64
}

func throughputSeries() []chartSeries {
	return []chartSeries{
		{Name: "Target", Color: colorTarget, Value: func(s map[string]any) float64 {
			f, _ := asFloat(s["target_rps"])
			return f
		}},
		{Name: "Achieved", Color: colorAchieved, Value: func(s map[string]any) float64 {
			f, _ := asFloat(s["achieved_rps"])
			return f
		}},
	}
}

func drawKPICards(pdf *fpdf.Fpdf, steps []map[string]any) {
	peakTarget, peakAchieved, worstP99, errRate := summarizeSteps(steps)
	cards := []struct{ label, value, hint string }{
		{"Peak achieved", fmt.Sprintf("%.0f /s", peakAchieved), "best step throughput"},
		{"Peak target", fmt.Sprintf("%.0f /s", peakTarget), "offered load"},
		{"Worst p99", fmt.Sprintf("%.1f ms", worstP99), "across load steps"},
		{"Error rate", fmt.Sprintf("%.1f%%", errRate*100), "failed writes"},
	}
	gap := 3.0
	w := (pdfWidth - gap*3) / 4
	y := pdf.GetY()
	for i, card := range cards {
		x := pdfMargin + float64(i)*(w+gap)
		setRGB(pdf, colorCardBG, true)
		pdf.RoundedRect(x, y, w, 22, 2, "1234", "F")
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
		pdf.CellFormat(w-6, 4, card.hint, "", 0, "L", false, 0, "")
	}
	pdf.SetY(y + 24)
}

func summarizeSteps(steps []map[string]any) (peakTarget, peakAchieved, worstP99, errRate float64) {
	var total, errs float64
	for _, step := range steps {
		if t, ok := asFloat(step["target_rps"]); ok && t > peakTarget {
			peakTarget = t
		}
		if a, ok := asFloat(step["achieved_rps"]); ok && a > peakAchieved {
			peakAchieved = a
		}
		w := asMap(step["writers"])
		if p, ok := asFloat(w["p99_ms"]); ok && p > worstP99 {
			worstP99 = p
		}
		c, _ := asFloat(w["count"])
		e, _ := asFloat(w["errors"])
		total += c
		errs += e
	}
	if total > 0 {
		errRate = errs / total
	}
	return
}

func maxHeldRate(steps []map[string]any) float64 {
	var held float64
	for _, step := range steps {
		target, _ := asFloat(step["target_rps"])
		achieved, _ := asFloat(step["achieved_rps"])
		w := asMap(step["writers"])
		count, _ := asFloat(w["count"])
		errs, _ := asFloat(w["errors"])
		if target <= 0 {
			continue
		}
		ok := achieved >= 0.9*target && (count == 0 || errs/count < 0.05)
		if ok && target > held {
			held = target
		}
	}
	return held
}

func drawGroupedBarChart(pdf *fpdf.Fpdf, title string, steps []map[string]any, series []chartSeries) {
	if len(steps) == 0 || len(series) == 0 {
		return
	}
	sectionTitle(pdf, title)
	chartTop := pdf.GetY() + 2
	chartLeft := pdfMargin + 12
	chartW := pdfWidth - 18
	chartH := 52.0
	chartBottom := chartTop + chartH
	maxV := 0.0
	for _, step := range steps {
		for _, s := range series {
			if v := s.Value(step); v > maxV {
				maxV = v
			}
		}
	}
	if maxV <= 0 {
		maxV = 1
	}
	maxV = niceMax(maxV)
	drawChartGrid(pdf, chartLeft, chartTop, chartW, chartH, maxV)

	n := float64(len(steps))
	groupW := chartW / n
	barGap := 1.2
	barW := (groupW - 8 - barGap*float64(len(series)-1)) / float64(len(series))
	if barW > 14 {
		barW = 14
	}
	if barW < 3 {
		barW = 3
	}
	for i, step := range steps {
		groupX := chartLeft + float64(i)*groupW + (groupW-barW*float64(len(series))-barGap*float64(len(series)-1))/2
		for j, s := range series {
			v := s.Value(step)
			h := (v / maxV) * chartH
			if h < 0 {
				h = 0
			}
			x := groupX + float64(j)*(barW+barGap)
			pdf.SetFillColor(s.Color[0], s.Color[1], s.Color[2])
			pdf.Rect(x, chartBottom-h, barW, h, "F")
		}
		label := fit(fmt.Sprint(step["label"]), groupW-2)
		pdf.SetFont("Helvetica", "", 6.5)
		setRGB(pdf, colorInk, false)
		pdf.SetXY(chartLeft+float64(i)*groupW, chartBottom+1.5)
		pdf.CellFormat(groupW, 4, pdfSafe(label), "", 0, "C", false, 0, "")
	}
	drawLegend(pdf, chartLeft, chartBottom+8, seriesNames(series))
}

func drawLineChart(pdf *fpdf.Fpdf, title, yCaption string, xs []float64, series []struct {
	Name  string
	Color [3]int
	Ys    []float64
}) {
	if len(xs) == 0 || len(series) == 0 {
		return
	}
	sectionTitle(pdf, title)
	chartTop := pdf.GetY() + 2
	chartLeft := pdfMargin + 12
	chartW := pdfWidth - 18
	chartH := 52.0
	chartBottom := chartTop + chartH
	minX, maxX := xs[0], xs[0]
	maxY := 0.0
	for _, x := range xs {
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
	}
	for _, s := range series {
		for _, y := range s.Ys {
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX <= minX {
		maxX = minX + 1
	}
	if maxY <= 0 {
		maxY = 1
	}
	maxY = niceMax(maxY)
	drawChartGrid(pdf, chartLeft, chartTop, chartW, chartH, maxY)
	xPos := func(x float64) float64 { return chartLeft + (x-minX)/(maxX-minX)*chartW }
	yPos := func(v float64) float64 { return chartBottom - (v/maxY)*chartH }
	for _, s := range series {
		pdf.SetDrawColor(s.Color[0], s.Color[1], s.Color[2])
		pdf.SetFillColor(s.Color[0], s.Color[1], s.Color[2])
		pdf.SetLineWidth(0.8)
		for i := range xs {
			if i >= len(s.Ys) {
				break
			}
			x, y := xPos(xs[i]), yPos(s.Ys[i])
			pdf.Circle(x, y, 1.1, "F")
			if i > 0 {
				pdf.Line(xPos(xs[i-1]), yPos(s.Ys[i-1]), x, y)
			}
		}
	}
	pdf.SetFont("Helvetica", "", 6.5)
	setRGB(pdf, colorInk, false)
	for i, x := range xs {
		if len(xs) > 8 && i != 0 && i != len(xs)-1 && i != len(xs)/2 {
			continue
		}
		pdf.SetXY(xPos(x)-8, chartBottom+1.5)
		pdf.CellFormat(16, 4, formatAxis(x), "", 0, "C", false, 0, "")
	}
	pdf.SetFont("Helvetica", "", 6)
	setRGB(pdf, colorMuted, false)
	pdf.SetXY(chartLeft, chartBottom+5.5)
	cap := yCaption
	if cap == "" {
		cap = "offered rate (req/s)"
	}
	pdf.CellFormat(chartW, 3.5, cap, "", 0, "C", false, 0, "")
	items := make([]legendItem, len(series))
	for i, s := range series {
		items[i] = legendItem{s.Name, s.Color}
	}
	drawLegend(pdf, chartLeft, chartBottom+10, items)
}

func drawLatencyScalingChart(pdf *fpdf.Fpdf, steps []map[string]any) {
	xs := make([]float64, 0, len(steps))
	p50 := make([]float64, 0, len(steps))
	p99 := make([]float64, 0, len(steps))
	for _, step := range steps {
		x, ok := asFloat(step["target_rps"])
		if !ok || x <= 0 {
			continue
		}
		w := asMap(step["writers"])
		a, _ := asFloat(w["p50_ms"])
		b, _ := asFloat(w["p99_ms"])
		xs = append(xs, x)
		p50 = append(p50, a)
		p99 = append(p99, b)
	}
	if len(xs) == 0 {
		return
	}
	drawLineChart(pdf, "Latency vs offered rate (ms)", "offered rate (req/s)", xs, []struct {
		Name  string
		Color [3]int
		Ys    []float64
	}{
		{"p50", colorP50, p50},
		{"p99", colorP99, p99},
	})
}

func drawAttainmentChart(pdf *fpdf.Fpdf, steps []map[string]any) {
	usable := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		target, _ := asFloat(step["target_rps"])
		if target > 0 {
			usable = append(usable, step)
		}
	}
	if len(usable) == 0 {
		return
	}
	sectionTitle(pdf, "Rate attainment (% of target held)")
	rowH := 7.0
	barMax := pdfWidth - 55
	for _, step := range usable {
		target, _ := asFloat(step["target_rps"])
		achieved, _ := asFloat(step["achieved_rps"])
		pct := 0.0
		if target > 0 {
			pct = achieved / target * 100
		}
		if pct > 100 {
			pct = 100
		}
		y := pdf.GetY()
		ensureSpace(pdf, rowH+2)
		pdf.SetFont("Helvetica", "", 7.5)
		setRGB(pdf, colorInk, false)
		pdf.SetXY(pdfMargin, y)
		pdf.CellFormat(40, rowH, pdfSafe(fit(fmt.Sprint(step["label"]), 40)), "", 0, "L", false, 0, "")
		setRGB(pdf, colorCardBG, true)
		pdf.Rect(pdfMargin+42, y+1.5, barMax, rowH-3, "F")
		fill := colorAchieved
		if pct < 90 {
			fill = colorDanger
		} else if pct < 97 {
			fill = colorP99
		}
		pdf.SetFillColor(fill[0], fill[1], fill[2])
		pdf.Rect(pdfMargin+42, y+1.5, barMax*(pct/100), rowH-3, "F")
		pdf.SetXY(pdfMargin+42+barMax+2, y)
		pdf.SetFont("Helvetica", "B", 7.5)
		setRGB(pdf, colorInk, false)
		pdf.CellFormat(12, rowH, fmt.Sprintf("%.0f%%", pct), "", 1, "L", false, 0, "")
	}
}

func drawChartGrid(pdf *fpdf.Fpdf, left, top, w, h, maxV float64) {
	bottom := top + h
	pdf.SetDrawColor(colorGrid[0], colorGrid[1], colorGrid[2])
	pdf.SetLineWidth(0.2)
	for i := 0; i <= 4; i++ {
		y := bottom - float64(i)*(h/4)
		pdf.Line(left, y, left+w, y)
		pdf.SetFont("Helvetica", "", 6.5)
		setRGB(pdf, colorMuted, false)
		pdf.SetXY(pdfMargin-1, y-2)
		pdf.CellFormat(12, 4, formatAxis(maxV*float64(i)/4), "", 0, "R", false, 0, "")
	}
	pdf.SetDrawColor(colorNavy[0], colorNavy[1], colorNavy[2])
	pdf.SetLineWidth(0.35)
	pdf.Line(left, top, left, bottom)
	pdf.Line(left, bottom, left+w, bottom)
}

type legendItem struct {
	Name  string
	Color [3]int
}

func seriesNames(series []chartSeries) []legendItem {
	out := make([]legendItem, len(series))
	for i, s := range series {
		out[i] = legendItem{s.Name, s.Color}
	}
	return out
}

func drawLegend(pdf *fpdf.Fpdf, x, y float64, items []legendItem) {
	pdf.SetY(y)
	lx := x
	for _, s := range items {
		pdf.SetFillColor(s.Color[0], s.Color[1], s.Color[2])
		pdf.Rect(lx, y+0.8, 4, 3, "F")
		pdf.SetXY(lx+5.5, y)
		pdf.SetFont("Helvetica", "", 7.5)
		setRGB(pdf, colorInk, false)
		pdf.Cell(28, 4.5, s.Name)
		lx += 34
	}
	pdf.SetY(y + 7)
}

func sectionTitle(pdf *fpdf.Fpdf, title string) {
	setRGB(pdf, colorNavy, false)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(pdfWidth, 7, title, "", 1, "L", false, 0, "")
}

func setRGB(pdf *fpdf.Fpdf, c [3]int, fill bool) {
	if fill {
		pdf.SetFillColor(c[0], c[1], c[2])
		return
	}
	pdf.SetTextColor(c[0], c[1], c[2])
}

func ensureSpace(pdf *fpdf.Fpdf, need float64) {
	_, _, _, bottom := pdf.GetMargins()
	if pdf.GetY()+need > 297-bottom {
		pdf.AddPage()
	}
}

func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(v))
	base := math.Pow(10, exp)
	n := v / base
	switch {
	case n <= 1:
		return base
	case n <= 2:
		return 2 * base
	case n <= 5:
		return 5 * base
	default:
		return 10 * base
	}
}

func formatAxis(v float64) string {
	switch {
	case v >= 1000:
		return fmt.Sprintf("%.0fk", v/1000)
	case v >= 10:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%.1f", v)
	}
}

func stepByTarget(steps []map[string]any) map[float64]map[string]any {
	out := map[float64]map[string]any{}
	for _, s := range steps {
		t, ok := asFloat(s["target_rps"])
		if ok {
			out[t] = s
		}
	}
	return out
}

func sortedTargets(a, b []map[string]any) []float64 {
	seen := map[float64]struct{}{}
	var xs []float64
	for _, steps := range [][]map[string]any{a, b} {
		for _, s := range steps {
			t, ok := asFloat(s["target_rps"])
			if !ok {
				continue
			}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			xs = append(xs, t)
		}
	}
	sort.Float64s(xs)
	return xs
}
