# LLM Context: DNS Zone Manager (Go)

Context for AI assistants working on this Go codebase.

## Critical: after changes

- Run `./tests.sh` (Go unit + frontend vitest).
- Fix failures before finishing.
- Prefer `go test -race ./...` for concurrency-sensitive packages.

## Critical: testing classes

1. Go unit tests (`go test ./internal/...`, `./cmd/...`)
2. Go integration (`//go:build integration` under `tests/integration/`, Docker)
3. Frontend unit (vitest)
4. Frontend Playwright (local stack; skipped in CI)

## Layout

```
cmd/dns-zone-manager/     # serve entrypoint
cmd/dns-cli/
internal/
  httpapi/                # routes, problem+json, auth middleware, SPA
  dnsx/                   # Client (TransferBackend), ZoneCache, Notify
  catalog/                # Catalog indexer (AXFR + poll + NOTIFY)
  store/ / scheduler/     # intent DB + apply loop
  config/                 # Settings, secret_file resolution
  ui/dist/                # embedded SPA (go:embed)
frontend/                 # Alpine.js SPA source
```

## Patterns

- Zone names always end with `.` — normalize at API boundary.
- DNS is source of truth; cache is ephemeral AXFR copy; DB holds scheduled
  intent + audit only.
- DDNS: add → NXRRSET; delete/replace → YXRRSET.
- Errors: RFC 9457 problem+json (`rcode` / `rcode_description` top-level).
- Catalog: if `settings.Catalog.Enabled`, `catalog.New` + `Start`, sync into
  cache via goroutine; wire `deps.Catalog`.

## Commands

```bash
make build test test-race lint frontend-build
go test ./...
go test -tags=integration ./tests/integration/...
npm run test && npm run typecheck
uv is NOT used — this is Go, not the Python tree.
```

## Config

`examples/config.example.yaml` — includes Go fields: `dns.pool_size`,
`dns.pool_idle_timeout`, `cache.serial_refresh_debounce`,
`metrics.per_zone_labels`, server timeouts, `live.ping_interval`, secret files.

## Gotchas

- Embed requires files under `internal/ui/dist` (placeholder committed).
- Typed-nil: never assign a nil `*catalog.Indexer` into `httpapi.CatalogIndexer`
  without going through a nil interface.
- Integration tags: default `go test ./...` skips `tests/integration`.
