package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var (
	allowedPurgeStatuses = map[string]struct{}{
		"applied": {}, "failed": {}, "cancelled": {}, "expired": {}, "reverted": {},
	}
	nonPurgeableStatuses = map[string]struct{}{
		"draft": {}, "scheduled": {}, "running": {},
	}
	logoSuffixes = map[string]struct{}{
		".svg": {}, ".png": {}, ".jpg": {}, ".jpeg": {}, ".webp": {},
	}
)

func validate(s *Settings) error {
	if strings.TrimSpace(s.DNS.Server) == "" {
		return fmt.Errorf("dns.server is required")
	}
	if s.Database.Backend == "sqlite" && s.Database.Path == "" {
		s.Database.Path = s.Scheduler.DatabasePath
	}
	if s.Database.Backend == "postgres" {
		if s.Database.Postgres == nil {
			return fmt.Errorf("database.backend is 'postgres' but no database.postgres section given")
		}
		if err := validatePostgres(s.Database.Postgres); err != nil {
			return err
		}
	}
	if len(s.TSIGKeys) > 0 {
		if err := validateTSIGRefs(s); err != nil {
			return err
		}
	}
	if err := validateRetention(&s.Retention); err != nil {
		return err
	}
	if err := validateWebhooks(&s.Webhooks); err != nil {
		return err
	}
	if err := validateThemeLogo(s.Theme.Logo); err != nil {
		return err
	}
	return nil
}

func validatePostgres(p *PostgresSettings) error {
	if !p.DSN.IsZero() {
		return nil
	}
	var missing []string
	if p.Host == "" {
		missing = append(missing, "host")
	}
	if p.Database == "" {
		missing = append(missing, "database")
	}
	if p.User == "" {
		missing = append(missing, "user")
	}
	if len(missing) > 0 {
		return fmt.Errorf("database.postgres requires either 'dsn' or host/database/user (missing: %s)", strings.Join(missing, ", "))
	}
	return nil
}

func validateTSIGRefs(s *Settings) error {
	names := make(map[string]struct{}, len(s.TSIGKeys))
	for _, k := range s.TSIGKeys {
		names[k.Name] = struct{}{}
	}
	check := func(field, name string) error {
		if name == "" {
			return nil
		}
		if _, ok := names[name]; !ok {
			list := make([]string, 0, len(names))
			for n := range names {
				list = append(list, n)
			}
			slices.Sort(list)
			return fmt.Errorf("%s references unknown key %q (available: %s)", field, name, strings.Join(list, ", "))
		}
		return nil
	}
	if err := check("dns.update_tsig_key", s.DNS.UpdateTSIGKey); err != nil {
		return err
	}
	if s.DNS.AXFRTSIGKey != nil {
		if err := check("dns.axfr_tsig_key", *s.DNS.AXFRTSIGKey); err != nil {
			return err
		}
	}
	if s.Notify.TSIGKey != nil {
		if err := check("notify.tsig_key", *s.Notify.TSIGKey); err != nil {
			return err
		}
	}
	return nil
}

func validateRetention(r *RetentionSettings) error {
	for _, st := range r.Statuses {
		if _, ok := nonPurgeableStatuses[st]; ok {
			return fmt.Errorf("retention.statuses cannot include active statuses: %s", st)
		}
		if _, ok := allowedPurgeStatuses[st]; !ok {
			if _, bad := nonPurgeableStatuses[st]; !bad {
				return fmt.Errorf("retention.statuses contains unknown value: %s", st)
			}
		}
	}
	return nil
}

func validateWebhooks(w *WebhookSettings) error {
	w.BaseURL = strings.TrimRight(w.BaseURL, "/")
	if w.Enabled && w.BaseURL == "" {
		return fmt.Errorf("webhooks.base_url is required when webhooks are enabled")
	}
	seen := map[string]struct{}{}
	for _, t := range w.Targets {
		if _, dup := seen[t.Name]; dup {
			return fmt.Errorf("duplicate webhook target names: %s", t.Name)
		}
		seen[t.Name] = struct{}{}
		if err := validateWebhookTargetURL(t); err != nil {
			return err
		}
	}
	return nil
}

func validateWebhookTargetURL(t WebhookTarget) error {
	raw := t.URL.String()
	if raw == "" {
		return fmt.Errorf("webhooks.targets[%s]: url is required", t.Name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("webhooks.targets[%s]: invalid url: %w", t.Name, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("webhooks.targets[%s]: url must be http or https, got %q", t.Name, scheme)
	}
	if scheme == "http" && !t.AllowInsecure {
		return fmt.Errorf("webhooks.targets[%s]: insecure http:// URL requires allow_insecure: true", t.Name)
	}
	return nil
}

func validateThemeLogo(logo *ThemeLogo) error {
	if logo == nil {
		return nil
	}
	hasURL := logo.URL != nil && strings.TrimSpace(*logo.URL) != ""
	hasPath := logo.Path != nil && strings.TrimSpace(*logo.Path) != ""
	if hasURL && hasPath {
		return fmt.Errorf("theme.logo.url and theme.logo.path are mutually exclusive")
	}
	if !hasURL && !hasPath {
		return fmt.Errorf("theme.logo requires either url or path")
	}
	if !hasPath {
		return nil
	}
	p := filepath.Clean(*logo.Path)
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("theme.logo.path does not exist or is not a file: %s", *logo.Path)
	}
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("theme.logo.path is not readable: %s", *logo.Path)
	}
	_ = f.Close()
	ext := strings.ToLower(filepath.Ext(p))
	if _, ok := logoSuffixes[ext]; !ok {
		return fmt.Errorf("theme.logo.path must end in .svg, .png, .jpg, .jpeg, or .webp, got %q", ext)
	}
	return nil
}
