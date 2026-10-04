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
  provision/            # optional rndc zone create/delete
  store/                # scheduled changes + audit (SQLite/Postgres)
  scheduler/            # apply loop
  config/               # YAML + env loading
  ui/dist/              # embedded SPA assets
frontend/               # TypeScript Alpine.js SPA (source)
tests/integration/      # //go:build integration
```

## Local loop

```bash
# Build the SPA, then serve API + UI from that directory on :8000
npx tsc && npx vite build
go run ./cmd/dns-zone-manager serve --config examples/config.yaml --ui-dir dist
```

Open http://localhost:8000. Browsers receive the SPA; clients that do not send
`Accept: text/html` still get the API info document at `/`.

`--ui-dir` reads the Vite build at runtime. Omit it to serve the assets
embedded in the binary (`make frontend-build`, which is what the container
image does). `npm run dev` (Vite on :5173) is optional for hot reload. The
devcontainer **Run All** task does not start it.

## Tests

```bash
go test ./...
go test -race ./...
go test -tags=integration ./tests/integration/...
npm run test
npm run typecheck
./tests.sh
./tests.sh --all
./tests.sh --report test-report.pdf   # same run, plus a PDF summary
```

Go integration tests (`//go:build integration`) need Docker. They cover BIND-backed
API checks and **dns-cli E2E** (`TestDNSCLI`): the CLI binary is built and run against
an httptest API with BIND + SQLite. Run only the CLI suite with:

```bash
go test -tags=integration ./tests/integration/ -count=1 -run DNSCLI
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
Compose uses `dns-zone-manager healthcheck`, which GETs that URL and exits 0
on HTTP 200.

## Forked miekg/dns

`go.mod` replaces `github.com/miekg/dns` with
[davidgroves/dns](https://github.com/davidgroves/dns) branch
`fix/tcp-tsig-response-mac` (commit `537ba7e9`).

miekg v1.1.63 does not chain the TSIG MAC across messages on one TCP
connection. BIND requires that chain, so the second signed update on a
pooled connection was `BADSIG`. The pool treated that as a broken socket
and closed it, so each connection carried one successful DDNS update and
then one failure. The retry of the failed update had already had its TSIG
record removed by signing, so it was sent unsigned and BIND refused it.
Half of a sustained run failed.

Drop the `replace` once the fix is in an upstream release and this module
requires that version.

## Migrations

Schema is applied at store open (goose). Point `database` at an empty SQLite
file or Postgres DSN; no manual step when `auto_migrate: true`.
