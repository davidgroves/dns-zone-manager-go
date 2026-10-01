package config

import (
	"os"
	"strings"

	"github.com/knadh/koanf/v2"
)

type envRoot struct {
	Prefix string
	Path   string
}

var envRoots = []envRoot{
	{Prefix: "DNS_", Path: "dns"},
	{Prefix: "API_", Path: "api_key"},
	{Prefix: "PROXY_AUTH_", Path: "proxy_auth"},
	{Prefix: "CACHE_", Path: "cache"},
	{Prefix: "CATALOG_", Path: "catalog"},
	{Prefix: "NOTIFY_", Path: "notify"},
	{Prefix: "SCHEDULER_", Path: "scheduler"},
	{Prefix: "DATABASE_", Path: "database"},
	{Prefix: "POSTGRES_", Path: "database.postgres"},
	{Prefix: "RETENTION_", Path: "retention"},
	{Prefix: "WEBHOOK_", Path: "webhooks"},
	{Prefix: "THEME_", Path: "theme"},
	{Prefix: "LOG_", Path: "logging"},
	{Prefix: "LIVE_", Path: "live"},
	{Prefix: "NSUPDATE_", Path: "nsupdate"},
	{Prefix: "SERVER_", Path: "server"},
	{Prefix: "METRICS_", Path: "metrics"},
}

func mergeEnv(k *koanf.Koanf) {
	for _, entry := range os.Environ() {
		envKey, val, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if envKey == "API_KEY_AUTH_ENABLED" || envKey == "API_KEYS" {
			continue
		}
		for _, root := range envRoots {
			if !strings.HasPrefix(envKey, root.Prefix) {
				continue
			}
			suffix := strings.TrimPrefix(envKey, root.Prefix)
			path := root.Path
			if suffix != "" {
				path += "." + envSuffixToKoanfPath(suffix)
			}
			_ = k.Set(path, val)
			break
		}
	}
	if v, ok := os.LookupEnv("API_KEY_AUTH_ENABLED"); ok {
		_ = k.Set("api_key.enabled", v)
	}
	if v, ok := os.LookupEnv("API_KEYS"); ok {
		_ = k.Set("api_key.keys_str", v)
	}
}

func envSuffixToKoanfPath(suffix string) string {
	suffix = strings.ToLower(suffix)
	return strings.ReplaceAll(suffix, "__", ".")
}
