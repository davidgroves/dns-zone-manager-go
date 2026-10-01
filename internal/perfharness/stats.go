package perfharness

import (
	"math"
	"sort"
	"sync"
)

// Latency collects millisecond samples and error counts.
type Latency struct {
	mu      sync.Mutex
	samples []float64
	errors  int
	kinds   map[string]int
}

func (l *Latency) Record(ms float64, errKind string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples = append(l.samples, ms)
	if errKind != "" {
		l.errors++
		if l.kinds == nil {
			l.kinds = map[string]int{}
		}
		l.kinds[errKind]++
	}
}

// Summary is the JSON shape stored under a step's "writers" object.
type Summary struct {
	Count        int            `json:"count"`
	Errors       int            `json:"errors"`
	ErrorKinds   map[string]int `json:"error_kinds,omitempty"`
	P50          float64        `json:"p50_ms,omitempty"`
	P95          float64        `json:"p95_ms,omitempty"`
	P99          float64        `json:"p99_ms,omitempty"`
	Max          float64        `json:"max_ms,omitempty"`
	Mean         float64        `json:"mean_ms,omitempty"`
	Min          float64        `json:"min_ms,omitempty"`
	IssuedTokens int            `json:"issued_tokens,omitempty"`
	WallSeconds  float64        `json:"wall_seconds,omitempty"`
	Mode         string         `json:"mode,omitempty"`
	Zones        int            `json:"zones,omitempty"`
}

func (l *Latency) Summary() Summary {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := Summary{Count: len(l.samples), Errors: l.errors, ErrorKinds: l.kinds}
	if len(l.samples) == 0 {
		return out
	}
	xs := append([]float64(nil), l.samples...)
	sort.Float64s(xs)
	sum := 0.0
	for _, v := range xs {
		sum += v
	}
	out.P50 = percentile(xs, 50)
	out.P95 = percentile(xs, 95)
	out.P99 = percentile(xs, 99)
	out.Min = xs[0]
	out.Max = xs[len(xs)-1]
	out.Mean = sum / float64(len(xs))
	return out
}

func percentile(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if pct <= 0 {
		return sorted[0]
	}
	if pct >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil(pct/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
