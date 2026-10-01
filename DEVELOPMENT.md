# Development (Go)

## Tooling

- Go **1.27+** (`go version`)
- Node **24** + npm (`npm ci`)
- Optional: `golangci-lint` v2+, Docker (integration), pre-commit

Pre-commit (installed in the devcontainer via `postCreateCommand`):

```bash
pre-commit install          # once per clone / container
pre-commit run --all-files  # gofmt, go vet, golangci-lint, tsc
```

Config: `.pre-commit-config.yaml` (Go/npm system hooks) and `.golangci.yml` (v2).

## Layout

```
cmd/dns-zone-manager/   # API server (cobra: serve)
cmd/dns-cli/            # CLI helper
internal/
  httpapi/              # Huma/chi HTTP API, middleware, SPA
  dnsx/                 # DDNS client, zone cache, NOTIFY
  catalog/              # RFC 9432 catalog indexer
  store/                # scheduled changes + audit (SQLite/Postgres)
  scheduler/            # apply loop
  config/               # YAML + env loading
  ui/dist/              # embedded SPA assets
frontend/               # TypeScript Alpine.js SPA (source)
tests/integration/      # //go:build integration
```

## Local loop

```bash
# Terminal A — API (reload yourself or use air if you prefer)
go run ./cmd/dns-zone-manager serve --config examples/config.yaml

# Terminal B — Vite proxy to :8000
npm run dev
```

Vite proxies `/v1`, `/health`, `/ui/*` to the Go server. For production-like
embeds: `make frontend-build` then run the binary alone.

## Tests

```bash
go test ./...
go test -race ./...
go test -tags=integration ./tests/integration/...
npm run test
npm run typecheck
./tests.sh
./tests.sh --all
```

## OpenAPI types

With the server listening:

```bash
npm run types:generate   # → frontend/types/api.d.ts
```

## Docker

```bash
make docker-build
docker run --rm -p 8000:8000 -v "$PWD/config.yaml:/config.yaml:ro" \
  dns-zone-manager:local serve --config /config.yaml
```

Distroless runtime: probe `/health` from the orchestrator (no curl in-image).

## Migrations

Schema is applied at store open (goose). Point `database` at an empty SQLite
file or Postgres DSN; no manual step when `auto_migrate: true`.
