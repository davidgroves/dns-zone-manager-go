package config

import (
	"net/url"
	"strconv"
	"strings"
)

func (s *Settings) GetTSIGKey(name string) *TSIGKeyEntry {
	for i := range s.TSIGKeys {
		if s.TSIGKeys[i].Name == name {
			return &s.TSIGKeys[i]
		}
	}
	return nil
}

func (s *Settings) GetUpdateTSIGKey() *TSIGKeyEntry {
	return s.GetTSIGKey(s.DNS.UpdateTSIGKey)
}

func (s *Settings) GetAXFRTSIGKey() *TSIGKeyEntry {
	name := s.DNS.UpdateTSIGKey
	if s.DNS.AXFRTSIGKey != nil {
		name = *s.DNS.AXFRTSIGKey
	}
	return s.GetTSIGKey(name)
}

func (s *Settings) GetNotifyTSIGKey() *TSIGKeyEntry {
	if s.Notify.TSIGKey != nil && *s.Notify.TSIGKey != "" {
		return s.GetTSIGKey(*s.Notify.TSIGKey)
	}
	return s.GetUpdateTSIGKey()
}

func (d *DNSSettings) EffectiveTCPPort() int {
	if d.TCPPort != nil {
		return *d.TCPPort
	}
	return d.Port
}

// EffectiveMaxZoneSizeBytes returns the per-zone size cap.
// Explicit MaxZoneSizeBytes wins; otherwise MaxSizeBytes is used.
func (c *CacheSettings) EffectiveMaxZoneSizeBytes() int64 {
	if c.MaxZoneSizeBytes > 0 {
		return c.MaxZoneSizeBytes
	}
	return c.MaxSizeBytes
}

func (d *DatabaseSettings) RedactedURL() string {
	if d.Backend == "postgres" && d.Postgres != nil {
		return d.Postgres.RedactedURL()
	}
	return "sqlite:///" + d.Path
}

func (p *PostgresSettings) URL() string {
	if !p.DSN.IsZero() {
		return normalizePostgresDSN(p.DSN.String())
	}
	user := url.QueryEscape(p.User)
	pass := url.QueryEscape(p.Password.String())
	creds := user
	if pass != "" {
		creds = user + ":" + pass
	}
	return "postgresql://" + creds + "@" + p.Host + ":" + strconv.Itoa(p.Port) + "/" + p.Database
}

func (p *PostgresSettings) RedactedURL() string {
	return redactURLPassword(p.URL())
}

func normalizePostgresDSN(dsn string) string {
	for _, prefix := range []string{"postgresql+psycopg://", "postgres+psycopg://"} {
		if strings.HasPrefix(dsn, prefix) {
			return "postgresql://" + strings.TrimPrefix(dsn, prefix)
		}
	}
	for _, prefix := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(dsn, prefix) {
			return "postgresql://" + strings.TrimPrefix(dsn, prefix)
		}
	}
	return dsn
}

func redactURLPassword(raw string) string {
	scheme, remainder, ok := strings.Cut(raw, "://")
	if !ok || !strings.Contains(remainder, "@") {
		return raw
	}
	userinfo, hostpart, ok := strings.Cut(remainder, "@")
	if !ok || !strings.Contains(userinfo, ":") {
		return raw
	}
	user, _, _ := strings.Cut(userinfo, ":")
	return scheme + "://" + user + ":***@" + hostpart
}
