package perfharness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

var metricLine = regexp.MustCompile(
	`^(?P<name>[a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(?P<labels>[^}]*)\})?\s+(?P<value>[-+]?(?:[0-9]*\.)?[0-9]+(?:[eE][-+]?\d+)?)`,
)

var interestingPrefixes = []string{
	"dns_zone_manager_ddns_updates_",
	"dns_zone_manager_rrset_",
	"dns_zone_manager_zone_transfers",
	"dns_zone_manager_cache_size_bytes",
	"dns_zone_manager_cache_evictions_total",
	"dns_zone_manager_notifies_",
	"dns_zone_manager_zone_ws_",
	"dns_zone_manager_webhook_",
}

// ParsePrometheus keeps interesting exposition lines as name{labels}=value.
func ParsePrometheus(text string) map[string]float64 {
	all := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := metricLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[metricLine.SubexpIndex("name")]
		labels := ""
		if i := metricLine.SubexpIndex("labels"); i >= 0 && i < len(m) {
			labels = m[i]
		}
		value, err := strconv.ParseFloat(m[metricLine.SubexpIndex("value")], 64)
		if err != nil {
			continue
		}
		key := name
		if labels != "" {
			key = name + "{" + labels + "}"
		}
		all[key] = value
		if _, ok := all[name]; !ok || labels == "" {
			all[name] = value
		}
	}
	out := map[string]float64{}
	for k, v := range all {
		base := k
		if i := strings.IndexByte(k, '{'); i >= 0 {
			base = k[:i]
		}
		for _, p := range interestingPrefixes {
			if strings.HasPrefix(base, p) {
				out[k] = v
				break
			}
		}
	}
	return out
}

// MetricsDelta is after-before, skipping Prometheus *_created timestamps.
func MetricsDelta(before, after map[string]float64) map[string]float64 {
	keys := map[string]struct{}{}
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	out := map[string]float64{}
	for _, k := range ordered {
		base := k
		if i := strings.IndexByte(k, '{'); i >= 0 {
			base = k[:i]
		}
		if strings.HasSuffix(base, "_created") {
			continue
		}
		out[k] = after[k] - before[k]
	}
	return out
}

func snapshotMetrics(cfg Config) (map[string]float64, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.APIBase, "/")+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("metrics HTTP %d", resp.StatusCode)
	}
	return ParsePrometheus(string(body)), nil
}

func cacheSizeBytes(cfg Config) *float64 {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.APIBase, "/")+"/metrics", nil)
	if err != nil {
		return nil
	}
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "dns_zone_manager_cache_size_bytes") && !strings.HasPrefix(line, "#") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				return nil
			}
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				return nil
			}
			return &v
		}
	}
	return nil
}

// QuerySOA asks BIND for the zone serial. Hostnames are resolved first.
func QuerySOA(host string, port int, zone string) (uint32, bool) {
	ip, err := resolveHost(host)
	if err != nil {
		return 0, false
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(zone), dns.TypeSOA)
	c := &dns.Client{Timeout: 2 * time.Second}
	resp, _, err := c.Exchange(m, net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil || resp == nil || resp.Rcode != dns.RcodeSuccess {
		return 0, false
	}
	for _, ans := range resp.Answer {
		if soa, ok := ans.(*dns.SOA); ok {
			return soa.Serial, true
		}
	}
	return 0, false
}

func appSerial(cfg Config, zone string) (uint32, bool) {
	u := strings.TrimRight(cfg.APIBase, "/") + "/v1/zones/" + url.PathEscape(trimDot(zone))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, false
	}
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var body struct {
		Serial *uint32 `json:"serial"`
		SOA    *struct {
			Serial *uint32 `json:"serial"`
		} `json:"soa"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, false
	}
	if body.Serial != nil {
		return *body.Serial, true
	}
	if body.SOA != nil && body.SOA.Serial != nil {
		return *body.SOA.Serial, true
	}
	return 0, false
}

// SampleSerial reads BIND and the API once. It does not wait for them to match.
func SampleSerial(cfg Config, zone string) map[string]any {
	out := map[string]any{"timed_out": false}
	if serial, ok := QuerySOA(cfg.BindHost, cfg.BindPort, zone); ok {
		out["bind_serial"] = serial
	} else {
		out["bind_serial"] = nil
	}
	if serial, ok := appSerial(cfg, zone); ok {
		out["app_serial"] = serial
	} else {
		out["app_serial"] = nil
	}
	return out
}

// MeasureSerialLag polls until the API serial catches BIND, or timeout.
func MeasureSerialLag(cfg Config, zone string, timeout time.Duration) map[string]any {
	bind, bindOK := QuerySOA(cfg.BindHost, cfg.BindPort, zone)
	started := time.Now()
	deadline := started.Add(timeout)
	var app uint32
	appOK := false
	for {
		app, appOK = appSerial(cfg, zone)
		if bindOK && appOK && app >= bind {
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	caught := bindOK && appOK && app >= bind
	out := map[string]any{
		"timed_out":       !caught,
		"timeout_seconds": timeout.Seconds(),
	}
	if bindOK {
		out["bind_serial"] = bind
	} else {
		out["bind_serial"] = nil
	}
	if appOK {
		out["app_serial"] = app
	} else {
		out["app_serial"] = nil
	}
	if caught {
		out["lag_seconds"] = time.Since(started).Seconds()
	} else {
		out["lag_seconds"] = nil
	}
	return out
}

func dockerStats() map[string]any {
	containers := []string{
		"dns-zone-manager_devcontainer-dev-1",
		"dns-zone-manager_devcontainer-bind-1",
		"dns-zone-manager-go_devcontainer-dev-1",
		"dns-zone-manager-go_devcontainer-bind-1",
		"dns-zone-manager",
		"dns-bind",
	}
	format := "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"stats", "--no-stream", "--format", format}, containers...)...)
	out, err := cmd.Output()
	if err != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()
		cmd = exec.CommandContext(ctx2, "docker", "stats", "--no-stream", "--format", format)
		out, err = cmd.Output()
		if err != nil {
			return nil
		}
	}
	wanted := map[string]struct{}{}
	for _, c := range containers {
		wanted[strings.ToLower(c)] = struct{}{}
	}
	result := map[string]any{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		name := parts[0]
		lower := strings.ToLower(name)
		_, listed := wanted[lower]
		if !listed && !strings.Contains(lower, "bind") && !strings.Contains(lower, "dns-zone") && !strings.Contains(lower, "dev-1") {
			continue
		}
		result[name] = map[string]any{
			"cpu_perc":  strings.TrimSpace(parts[1]),
			"mem_usage": strings.TrimSpace(parts[2]),
			"mem_perc":  strings.TrimSpace(parts[3]),
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// EnvironmentProbe is the before/after snapshot stored on a report.
// Serial is a single sample. The 30s catch-up poll runs only when a scenario
// sets measure_serial_lag.
func EnvironmentProbe(cfg Config, zone string) map[string]any {
	probe := map[string]any{
		"api_base":     cfg.APIBase,
		"bind_host":    cfg.BindHost,
		"bind_port":    cfg.BindPort,
		"docker_stats": dockerStats(),
		"host":         map[string]any{"cwd": mustGetwd()},
	}
	if metrics, err := snapshotMetrics(cfg); err != nil {
		probe["metrics_error"] = err.Error()
	} else {
		probe["metrics"] = metrics
	}
	if zone != "" {
		probe["serial"] = SampleSerial(cfg, zone)
	}
	return probe
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
