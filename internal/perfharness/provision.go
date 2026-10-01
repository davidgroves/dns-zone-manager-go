package perfharness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ProvisionResult is the timing record for zone create and destroy.
type ProvisionResult struct {
	Action               string   `json:"action" yaml:"action"`
	Zone                 string   `json:"zone" yaml:"zone"`
	Records              *int     `json:"records,omitempty" yaml:"records,omitempty"`
	ZonePath             string   `json:"zone_path,omitempty" yaml:"zone_path,omitempty"`
	GenerateSeconds      *float64 `json:"generate_seconds,omitempty" yaml:"generate_seconds,omitempty"`
	GenerateBytes        *int     `json:"generate_bytes,omitempty" yaml:"generate_bytes,omitempty"`
	BindAddSeconds       *float64 `json:"bind_add_seconds,omitempty" yaml:"bind_add_seconds,omitempty"`
	BindReadySeconds     *float64 `json:"bind_ready_seconds,omitempty" yaml:"bind_ready_seconds,omitempty"`
	AXFRRefreshSeconds   *float64 `json:"axfr_refresh_seconds,omitempty" yaml:"axfr_refresh_seconds,omitempty"`
	CacheSizeBytesBefore *float64 `json:"cache_size_bytes_before,omitempty" yaml:"cache_size_bytes_before,omitempty"`
	CacheSizeBytesAfter  *float64 `json:"cache_size_bytes_after,omitempty" yaml:"cache_size_bytes_after,omitempty"`
	SOASerial            *uint32  `json:"soa_serial,omitempty" yaml:"soa_serial,omitempty"`
	Errors               []string `json:"errors,omitempty" yaml:"errors,omitempty"`
}

func (p ProvisionResult) asMap() map[string]any {
	out := map[string]any{"action": p.Action, "zone": p.Zone}
	if p.Records != nil {
		out["records"] = *p.Records
	}
	putStr(out, "zone_path", p.ZonePath)
	putF(out, "generate_seconds", p.GenerateSeconds)
	if p.GenerateBytes != nil {
		out["generate_bytes"] = *p.GenerateBytes
	}
	putF(out, "bind_add_seconds", p.BindAddSeconds)
	putF(out, "bind_ready_seconds", p.BindReadySeconds)
	putF(out, "axfr_refresh_seconds", p.AXFRRefreshSeconds)
	putF(out, "cache_size_bytes_before", p.CacheSizeBytesBefore)
	putF(out, "cache_size_bytes_after", p.CacheSizeBytesAfter)
	if p.SOASerial != nil {
		out["soa_serial"] = *p.SOASerial
	}
	if len(p.Errors) > 0 {
		out["errors"] = p.Errors
	}
	return out
}

func putStr(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func putF(m map[string]any, k string, v *float64) {
	if v != nil {
		m[k] = *v
	}
}

func fsec(d time.Duration) *float64 {
	s := d.Seconds()
	return &s
}

// CreateZone generates a zone file when needed, loads it with rndc, and refreshes the API cache.
func CreateZone(cfg Config, zone string, records, seed int, skipGenerate, skipCache bool) ProvisionResult {
	zone = trimDot(zone)
	res := ProvisionResult{Action: "create", Zone: zone, Records: &records}
	path, err := defaultZonePath(zone)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	if !skipGenerate {
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			res.ZonePath = path
			n := int(fi.Size())
			res.GenerateBytes = &n
			zero := 0.0
			res.GenerateSeconds = &zero
		} else {
			gen, err := GenerateZone(zone, records, seed, path)
			if err != nil {
				res.Errors = append(res.Errors, err.Error())
				return res
			}
			res.ZonePath = gen.Path
			res.GenerateSeconds = fsec(gen.Elapsed)
			res.GenerateBytes = &gen.Bytes
			path = gen.Path
		}
	} else {
		res.ZonePath = path
	}
	if _, err := os.Stat(path); err != nil {
		res.Errors = append(res.Errors, "zone file missing: "+path)
		return res
	}
	started := time.Now()
	_, stderr, err := runRNDC(cfg, "addzone", zone, addzoneConfig(cfg, zone, path))
	res.BindAddSeconds = fsec(time.Since(started))
	if err != nil && !strings.Contains(strings.ToLower(stderr), "already exists") {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		res.Errors = append(res.Errors, "rndc addzone failed: "+msg)
		return res
	}
	ready, serial, ok := waitForSOA(cfg, zone, 900*time.Second)
	res.BindReadySeconds = fsec(ready)
	if !ok {
		res.Errors = append(res.Errors, "BIND did not answer SOA within timeout")
		return res
	}
	res.SOASerial = &serial
	if !skipCache {
		res.CacheSizeBytesBefore = cacheSizeBytes(cfg)
		t0 := time.Now()
		if err := refreshZoneCache(cfg, zone); err != nil {
			res.Errors = append(res.Errors, "cache refresh failed: "+err.Error())
		} else {
			res.AXFRRefreshSeconds = fsec(time.Since(t0))
		}
		res.CacheSizeBytesAfter = cacheSizeBytes(cfg)
	}
	return res
}

// DestroyZone removes the zone from BIND and, when asked, deletes the zone file.
func DestroyZone(cfg Config, zone string, deleteFile bool) ProvisionResult {
	zone = trimDot(zone)
	res := ProvisionResult{Action: "destroy", Zone: zone}
	started := time.Now()
	_, stderr, err := runRNDC(cfg, "delzone", "-clean", zone)
	res.BindAddSeconds = fsec(time.Since(started))
	if err != nil {
		low := strings.ToLower(stderr)
		if !strings.Contains(low, "not found") && !strings.Contains(low, "no matching zone") {
			msg := strings.TrimSpace(stderr)
			if msg == "" {
				msg = err.Error()
			}
			res.Errors = append(res.Errors, "rndc delzone failed: "+msg)
		}
	}
	zp := url.PathEscape(zone)
	req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(cfg.APIBase, "/")+"/v1/zones/"+zp+"/cache", nil)
	if err == nil {
		if cfg.APIKey != "" {
			req.Header.Set("X-API-Key", cfg.APIKey)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
	if deleteFile {
		path, err := defaultZonePath(zone)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			return res
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			res.Errors = append(res.Errors, "delete file failed: "+err.Error())
		}
		for _, suffix := range []string{".jnl", ".jbk", ".signed"} {
			_ = os.Remove(path + suffix)
		}
	}
	return res
}

func addzoneConfig(cfg Config, zone, zoneFile string) string {
	_ = zone
	return fmt.Sprintf(`{ type primary; file "%s"; allow-update { key "%s"; }; allow-transfer { key "%s"; }; };`,
		zoneFile, cfg.TSIGName, cfg.TSIGName)
}

func runRNDC(cfg Config, args ...string) (string, string, error) {
	cmdArgs := []string{}
	if fi, err := os.Stat(cfg.RNDCConf); err == nil && !fi.IsDir() {
		cmdArgs = append(cmdArgs, "-c", cfg.RNDCConf)
	}
	cmdArgs = append(cmdArgs, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.RNDC, cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func waitForSOA(cfg Config, zone string, timeout time.Duration) (time.Duration, uint32, bool) {
	started := time.Now()
	deadline := started.Add(timeout)
	for {
		if serial, ok := QuerySOA(cfg.BindHost, cfg.BindPort, zone); ok {
			return time.Since(started), serial, true
		}
		if !time.Now().Before(deadline) {
			return time.Since(started), 0, false
		}
		time.Sleep(time.Second)
	}
}

func refreshZoneCache(cfg Config, zone string) error {
	base := strings.TrimRight(cfg.APIBase, "/")
	zp := url.PathEscape(trimDot(zone))
	client := &http.Client{Timeout: 600 * time.Second}
	del, err := http.NewRequest(http.MethodDelete, base+"/v1/zones/"+zp+"/cache", nil)
	if err != nil {
		return err
	}
	if cfg.APIKey != "" {
		del.Header.Set("X-API-Key", cfg.APIKey)
	}
	if resp, err := client.Do(del); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	post, err := http.NewRequest(http.MethodPost, base+"/v1/zones/"+zp+"/refresh", nil)
	if err != nil {
		return err
	}
	if cfg.APIKey != "" {
		post.Header.Set("X-API-Key", cfg.APIKey)
	}
	resp, err := client.Do(post)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
