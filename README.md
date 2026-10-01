# DNS Zone Manager (Go)

Go rewrite of the DNS Zone Manager API and embedded SPA. Manage BIND (or other
DDNS/AXFR) zones over a REST API with scheduled changes, catalog discovery,
live WebSocket updates, and Prometheus metrics.

## Requirements

- **Go 1.27+**
- **Node.js 24+** (frontend build / tests)
- A DNS server with TSIG-authenticated DDNS + AXFR (BIND 9.x recommended)

## Quick start

```bash
# Config
cp examples/config.example.yaml config.yaml
# edit dns.server, tsig_keys, api_key…

# Build SPA into the embed path (optional for `go run` if placeholder is enough)
make frontend-build

# Run
make run
# or: go run ./cmd/dns-zone-manager serve --config config.yaml
```

Open http://localhost:8000 — the binary serves `/health`, `/ready`, `/metrics`,
`/v1/…`, and the embedded SPA.

## Make targets

| Target | Description |
|--------|-------------|
| `make build` | Build `bin/dns-zone-manager` and `bin/dns-cli` |
| `make test` | `go test ./…` |
| `make test-race` | Race detector |
| `make test-integration` | `go test -tags=integration ./tests/integration/…` |
| `make lint` | golangci-lint |
| `make fmt` / `make tidy` | Format + tidy |
| `make run` | Serve with `examples/config.yaml` |
| `make frontend-build` | Vite build → `internal/ui/dist` |
| `make docker-build` | Multi-stage Docker image |
| `make types-generate` | OpenAPI → `frontend/types/api.d.ts` |

```bash
./tests.sh          # Go unit + npm vitest
./tests.sh --all    # + integration + Playwright when servers are up
```

## Configuration

YAML (preferred) or environment variables. See
[`examples/config.example.yaml`](examples/config.example.yaml).

Notable Go-side settings: `dns.pool_size`, `dns.pool_idle_timeout`,
`cache.serial_refresh_debounce`, `metrics.per_zone_labels`, `server.*` timeouts,
`live.ping_interval`, and `secret_file` / `*_file` secret refs.

## Differences from the Python service

- Errors use **RFC 9457 problem+json** (`type`, `title`, `detail`, `rcode`, …)
- List endpoints use **opaque cursors** (not offset pages alone)
- SPA is **embedded** in the binary (`internal/ui`); no separate nginx required
- **`/ready`** distinguishes liveness (`/health`) from dependency readiness
- Single static binary + optional `dns-cli`

## License

Same as the upstream project.
