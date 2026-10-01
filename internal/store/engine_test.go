package store

import (
	"net/url"
	"strings"
	"testing"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

func TestPostgresDSNOptionsEncoding(t *testing.T) {
	var pass config.Secret
	pass.Set("devpassword")
	p := &config.PostgresSettings{
		Host:               "postgres",
		Port:               5432,
		Database:           "dns_zone_manager",
		User:               "dns_zone_manager",
		Password:           pass,
		SSLMode:            "disable",
		Schema:             "public",
		StatementTimeoutMS: 30000,
	}
	dsn, err := postgresDSN(p)
	if err != nil {
		t.Fatalf("postgresDSN: %v", err)
	}
	if strings.Contains(dsn, "+search_path") {
		t.Fatalf("DSN still has +search_path (broken encode): %s", dsn)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	opts := u.Query().Get("options")
	if !strings.Contains(opts, "search_path=public") {
		t.Fatalf("options missing search_path: %q (dsn=%s)", opts, dsn)
	}
	if !strings.Contains(opts, "statement_timeout=30000") {
		t.Fatalf("options missing statement_timeout: %q (dsn=%s)", opts, dsn)
	}
	if strings.Contains(opts, "+search_path") {
		t.Fatalf("decoded options still have +search_path: %q", opts)
	}
}
