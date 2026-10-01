package perfharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

// StepResult is one rate step, matching the JSON the reports already use.
type StepResult struct {
	Label           string  `json:"label"`
	TargetRPS       float64 `json:"target_rps"`
	DurationSeconds float64 `json:"duration_seconds"`
	AchievedRPS     float64 `json:"achieved_rps"`
	Writers         any     `json:"writers"`
	Readers         any     `json:"readers,omitempty"`
	Websocket       any     `json:"websocket,omitempty"`
}

// StepSpec is one entry under load.steps in a scenario file.
type StepSpec struct {
	RPS            float64  `yaml:"rps" json:"rps"`
	Duration       float64  `yaml:"duration" json:"duration"`
	Label          string   `yaml:"label" json:"label"`
	Concurrency    *int     `yaml:"concurrency" json:"concurrency,omitempty"`
	PoolSize       *int     `yaml:"pool_size" json:"pool_size,omitempty"`
	Readers        *bool    `yaml:"readers" json:"readers,omitempty"`
	ReaderRPS      *float64 `yaml:"reader_rps" json:"reader_rps,omitempty"`
	WebsocketCount *int     `yaml:"websocket_count" json:"websocket_count,omitempty"`
}

// LoadOptions are the defaults a scenario's load block applies to every step.
type LoadOptions struct {
	Mode           string
	Concurrency    int
	PoolSize       int
	Readers        bool
	ReaderRPS      float64
	WebsocketCount int
	Zones          []string
	Steps          []StepSpec
}

// TargetZones returns the zones a write step cycles through.
func TargetZones(zone string, zones []string) []string {
	var picked []string
	for _, z := range zones {
		z = strings.TrimSpace(z)
		if z != "" {
			picked = append(picked, z)
		}
	}
	if len(picked) == 0 {
		return []string{zone}
	}
	return picked
}

// ZoneForSeq is the zone for a 1-based token, round-robin.
func ZoneForSeq(targets []string, seq int) string {
	if len(targets) == 0 {
		return ""
	}
	idx := seq - 1
	if idx < 0 {
		idx = 0
	}
	return targets[idx%len(targets)]
}

func writeName(seq, pool int) string {
	if pool < 1 {
		pool = 1
	}
	return fmt.Sprintf("perfchg-%06d", (seq%pool)+1)
}

func writeAddr(seq int) string {
	return fmt.Sprintf("203.0.113.%d", (seq%200)+1)
}

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.code, e.body)
}

func errKind(err error) string {
	if err == nil {
		return ""
	}
	var statusErr *statusError
	if errors.As(err, &statusErr) {
		return "RuntimeError"
	}
	t := reflect.TypeOf(err)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Name() == "" {
		return "error"
	}
	return t.Name()
}

// RunRamp runs each rate step in order.
func RunRamp(cfg Config, zone string, opt LoadOptions, showProgress bool) []StepResult {
	if showProgress && !isatty(os.Stderr) {
		showProgress = false
	}
	out := make([]StepResult, 0, len(opt.Steps))
	for i, step := range opt.Steps {
		label := step.Label
		if label == "" {
			label = fmt.Sprintf("%s@%g/s", opt.Mode, step.RPS)
		}
		duration := step.Duration
		if duration <= 0 {
			duration = 60
		}
		conc := opt.Concurrency
		if step.Concurrency != nil {
			conc = *step.Concurrency
		}
		pool := opt.PoolSize
		if step.PoolSize != nil {
			pool = *step.PoolSize
		}
		readers := opt.Readers
		if step.Readers != nil {
			readers = *step.Readers
		}
		readerRPS := opt.ReaderRPS
		if step.ReaderRPS != nil {
			readerRPS = *step.ReaderRPS
		}
		wsCount := opt.WebsocketCount
		if step.WebsocketCount != nil {
			wsCount = *step.WebsocketCount
		}
		res := runStep(cfg, stepInput{
			zone: zone, mode: opt.Mode, zones: opt.Zones,
			targetRPS: step.RPS, duration: duration, label: label,
			concurrency: conc, poolSize: pool,
			readers: readers, readerRPS: readerRPS, websocketCount: wsCount,
			progress: showProgress, stepIndex: i + 1, stepTotal: len(opt.Steps),
		})
		out = append(out, res)
	}
	return out
}

type stepInput struct {
	zone, mode, label     string
	zones                 []string
	targetRPS, duration   float64
	readerRPS             float64
	concurrency, poolSize int
	websocketCount        int
	readers, progress     bool
	stepIndex, stepTotal  int
}

func runStep(cfg Config, in stepInput) StepResult {
	stats := &Latency{}
	var wall float64
	var issued int
	var readers map[string]any
	var ws map[string]any
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		wall, issued = runWriters(cfg, in, stats)
	}()
	if in.readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			readers = runReaders(cfg, in.zone, time.Duration(in.duration*float64(time.Second)), in.readerRPS)
		}()
	}
	if in.websocketCount > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ws = runWebsocket(cfg, in.zone, in.websocketCount, time.Duration(in.duration*float64(time.Second)))
		}()
	}

	stop := make(chan struct{})
	progressDone := make(chan struct{})
	if in.progress {
		go func() {
			defer close(progressDone)
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			started := time.Now()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					n, errs := stats.Counts()
					elapsed := time.Since(started).Seconds()
					fmt.Fprintf(os.Stderr, "\r[%d/%d] %s  %.0fs/%.0fs  n=%d err=%d",
						in.stepIndex, in.stepTotal, in.label, elapsed, in.duration, n, errs)
				}
			}
		}()
	} else {
		close(progressDone)
	}
	wg.Wait()
	close(stop)
	<-progressDone

	summary := stats.Summary()
	summary.IssuedTokens = issued
	summary.WallSeconds = wall
	summary.Mode = in.mode
	summary.Zones = len(TargetZones(in.zone, in.zones))
	achieved := 0.0
	if wall > 0 {
		achieved = float64(summary.Count) / wall
	}
	if in.progress {
		fmt.Fprintf(os.Stderr, "\r[%d/%d] %s  done  achieved=%.1f/s  n=%d  err=%d  p99=%.1fms\n",
			in.stepIndex, in.stepTotal, in.label, achieved, summary.Count, summary.Errors, summary.P99)
	}
	return StepResult{
		Label: in.label, TargetRPS: in.targetRPS, DurationSeconds: in.duration,
		AchievedRPS: achieved, Writers: summary, Readers: readers, Websocket: ws,
	}
}

func (l *Latency) Counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.samples), l.errors
}

func runWriters(cfg Config, in stepInput, stats *Latency) (wall float64, issued int) {
	started := time.Now()
	defer func() { wall = time.Since(started).Seconds() }()

	if in.concurrency < 1 {
		in.concurrency = 1
	}
	if in.poolSize < 1 {
		in.poolSize = 1
	}
	targets := TargetZones(in.zone, in.zones)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        in.concurrency * 2,
			MaxIdleConnsPerHost: in.concurrency,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	bindIP, bindErr := resolveHost(cfg.BindHost)
	dnsClient := &dns.Client{
		Net:     "tcp",
		Timeout: 10 * time.Second,
	}
	tsig := map[string]string{dns.Fqdn(cfg.TSIGName): cfg.TSIGSecret}

	sem := make(chan struct{}, in.concurrency)
	var wg sync.WaitGroup

	if in.targetRPS <= 0 {
		time.Sleep(time.Duration(in.duration * float64(time.Second)))
		return wall, 0
	}
	interval := time.Duration(float64(time.Second) / in.targetRPS)
	deadline := time.Now().Add(time.Duration(in.duration * float64(time.Second)))
	next := time.Now()
	for {
		now := time.Now()
		if !now.Before(deadline) {
			break
		}
		if now.Before(next) {
			sleep := next.Sub(now)
			if remain := deadline.Sub(now); sleep > remain {
				sleep = remain
			}
			time.Sleep(sleep)
			continue
		}
		issued++
		seq := issued
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := writeName(seq, in.poolSize)
			addr := writeAddr(seq)
			zone := ZoneForSeq(targets, seq)
			useAPI := in.mode == "api" || (in.mode == "mixed" && seq%2 == 0)
			t0 := time.Now()
			var kind string
			sem <- struct{}{}
			if useAPI {
				if err := apiReplace(client, cfg, zone, name, addr); err != nil {
					kind = errKind(err)
				}
			} else if bindErr != nil {
				kind = errKind(bindErr)
			} else if err := ddnsReplace(dnsClient, tsig, cfg, bindIP, zone, name, addr); err != nil {
				kind = errKind(err)
			}
			<-sem
			stats.Record(float64(time.Since(t0).Microseconds())/1000.0, kind)
		}()
		next = next.Add(interval)
		if next.Before(time.Now().Add(-interval)) {
			next = time.Now()
		}
	}
	wg.Wait()
	return wall, issued
}

func apiReplace(client *http.Client, cfg Config, zone, name, addr string) error {
	raw, err := json.Marshal(map[string]any{
		"name": name, "ttl": 60, "type": "A", "rdclass": "IN", "records": []string{addr},
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(cfg.APIBase, "/") + "/v1/zones/" + url.PathEscape(trimDot(zone)) + "/rrsets"
	do := func(method string) (*http.Response, error) {
		req, err := http.NewRequest(method, endpoint, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			req.Header.Set("X-API-Key", cfg.APIKey)
		}
		return client.Do(req)
	}
	resp, err := do(http.MethodPut)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		resp, err = do(http.MethodPost)
		if err != nil {
			return err
		}
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &statusError{code: resp.StatusCode, body: string(snippet)}
	}
	return nil
}

func ddnsReplace(client *dns.Client, secret map[string]string, cfg Config, bindIP, zone, name, addr string) error {
	owner := dns.Fqdn(name + "." + trimDot(zone))
	m := new(dns.Msg)
	m.SetUpdate(dns.Fqdn(zone))
	m.RemoveRRset([]dns.RR{&dns.ANY{Hdr: dns.RR_Header{Name: owner, Rrtype: dns.TypeA}}})
	rr, err := dns.NewRR(fmt.Sprintf("%s 60 IN A %s", owner, addr))
	if err != nil {
		return err
	}
	m.Insert([]dns.RR{rr})
	keyName := dns.Fqdn(cfg.TSIGName)
	m.SetTsig(keyName, dnsx.AlgorithmFromString(cfg.TSIGAlg), 300, time.Now().Unix())
	resp, _, err := client.Exchange(m, net.JoinHostPort(bindIP, fmt.Sprintf("%d", cfg.BindPort)))
	if err != nil {
		return err
	}
	if resp == nil || resp.Rcode != dns.RcodeSuccess {
		rc := "nil"
		if resp != nil {
			rc = dns.RcodeToString[resp.Rcode]
		}
		return &statusError{code: 0, body: "DDNS rcode=" + rc}
	}
	return nil
}

func resolveHost(host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no addresses for %s", host)
	}
	return ips[0].String(), nil
}

func runReaders(cfg Config, zone string, duration time.Duration, opsPerSec float64) map[string]any {
	kinds := []string{"first_page", "deep_page", "search", "zone_list", "export"}
	byKind := map[string]*Latency{}
	for _, k := range kinds {
		byKind[k] = &Latency{}
	}
	client := &http.Client{Timeout: 120 * time.Second}
	base := strings.TrimRight(cfg.APIBase, "/")
	zp := url.PathEscape(trimDot(zone))
	after := "host0001000"
	stop := time.Now().Add(duration)
	interval := time.Duration(float64(time.Second) / opsPerSec)
	if opsPerSec <= 0 {
		interval = duration
	}
	seq := 0
	for time.Now().Before(stop) {
		kind := kinds[seq%len(kinds)]
		seq++
		t0 := time.Now()
		kindErr := readerOnce(client, cfg, base, zp, after, kind)
		byKind[kind].Record(float64(time.Since(t0).Microseconds())/1000.0, errKind(kindErr))
		remain := time.Until(stop)
		if remain <= 0 {
			break
		}
		if interval < remain {
			time.Sleep(interval)
		} else {
			time.Sleep(remain)
		}
	}
	out := map[string]any{}
	for k, st := range byKind {
		out[k] = st.Summary()
	}
	return out
}

func readerOnce(client *http.Client, cfg Config, base, zp, after, kind string) error {
	var u string
	switch kind {
	case "first_page":
		u = fmt.Sprintf("%s/v1/zones/%s/rrsets?limit=100", base, zp)
	case "deep_page":
		u = fmt.Sprintf("%s/v1/zones/%s/rrsets?limit=100&after=%s", base, zp, url.QueryEscape(after))
	case "search":
		u = fmt.Sprintf("%s/v1/zones/%s/search?name_pattern=host0001&limit=50", base, zp)
	case "zone_list":
		u = base + "/v1/zones?limit=100"
	default:
		u = fmt.Sprintf("%s/v1/zones/%s/export", base, zp)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &statusError{code: resp.StatusCode}
	}
	return nil
}

func runWebsocket(cfg Config, zone string, count int, duration time.Duration) map[string]any {
	base := strings.TrimRight(cfg.APIBase, "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	wsURL := base + "/v1/zones/" + url.PathEscape(trimDot(zone)) + "/ws"
	if cfg.APIKey != "" {
		wsURL += "?api_key=" + url.QueryEscape(cfg.APIKey)
	}
	var messages, errs, connected int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), duration+20*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, wsURL, nil)
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err != nil {
				mu.Lock()
				errs++
				mu.Unlock()
				return
			}
			mu.Lock()
			connected++
			mu.Unlock()
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
			deadline := time.Now().Add(duration)
			for time.Now().Before(deadline) {
				rctx, rcancel := context.WithTimeout(ctx, time.Second)
				_, _, err := conn.Read(rctx)
				rcancel()
				if err == nil {
					mu.Lock()
					messages++
					mu.Unlock()
					continue
				}
				if rctx.Err() != nil && time.Now().Before(deadline) {
					continue
				}
				mu.Lock()
				errs++
				mu.Unlock()
				return
			}
		}()
	}
	wg.Wait()
	return map[string]any{
		"enabled": true, "requested": count, "connected": connected,
		"messages": messages, "errors": errs, "duration_seconds": duration.Seconds(),
	}
}

func isatty(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
