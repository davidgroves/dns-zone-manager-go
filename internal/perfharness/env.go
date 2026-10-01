// Package perfharness runs the DNS Zone Manager performance scenarios.
package perfharness

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the process environment the harness reads once at startup.
type Config struct {
	APIBase    string
	APIKey     string
	BindHost   string
	BindPort   int
	TSIGName   string
	TSIGSecret string
	TSIGAlg    string
	RNDC       string
	RNDCConf   string
}

// ConfigFromEnv loads harness settings from the environment, with the same
// defaults as the previous Python tool.
func ConfigFromEnv() Config {
	port := 15353
	if v := os.Getenv("BIND_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	return Config{
		APIBase:    envOr("API_BASE", "http://127.0.0.1:8000"),
		APIKey:     os.Getenv("API_KEY"),
		BindHost:   envOr("BIND_HOST", "bind"),
		BindPort:   port,
		TSIGName:   envOr("TSIG_NAME", "dns-api-key"),
		TSIGSecret: envOr("TSIG_SECRET", "K8vC2mP9nQ4rT6wX1yB3fG5hJ7kL0mN2pR4sU6vW8xY="),
		TSIGAlg:    envOr("TSIG_ALG", "hmac-sha256"),
		RNDC:       envOr("RNDC", "rndc"),
		RNDCConf:   envOr("RNDC_CONF", "/etc/rndc.conf"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func trimDot(zone string) string { return strings.TrimRight(zone, ".") }

// Presets are the named zone sizes used by zone create and generate.
var Presets = map[string]int{
	"100k": 100_000,
	"1m":   1_000_000,
	"5m":   5_000_000,
}

// DefaultZone is the large zone the write scenarios were originally aimed at.
const DefaultZone = "perf5m.test"

// ResolveRecords picks an explicit count, otherwise a preset (default 5m).
func ResolveRecords(preset string, records int) (int, error) {
	if records > 0 {
		return records, nil
	}
	if preset == "" {
		preset = "5m"
	}
	n, ok := Presets[preset]
	if !ok {
		return 0, fmt.Errorf("unknown preset %q", preset)
	}
	return n, nil
}
