# Architecture (Go)

DNS remains the sole source of truth for zone contents. This service speaks
AXFR / IXFR / DDNS / NOTIFY / TSIG to a hidden primary (typically BIND) and
keeps an ephemeral in-memory cache. SQLite or PostgreSQL stores only scheduled
change *intent*, audit events, and the durable webhook outbox.

```
Clients ──► Huma/stdlib HTTP + WS ──► dnsx.Client (TCP pool) ──► BIND
                 │                         │
                 ├── ZoneCache (sorted index, LRU)
                 ├── store (bun) ◄── scheduler runner
                 ├── notifications outbox worker
                 └── live Hub (per-subscriber outboxes)
```

## Throughput path

Each DDNS UPDATE reuses a pooled TCP connection to BIND. Request handlers never
block on store writes for webhooks (outbox enqueue is async relative to the
BIND round-trip attribution). Per-zone RWMutex + sorted owner/type index keep
pagination and optimistic cache updates cheap.

Success criterion: REST write rate within a few percent of direct DDNS against
the same BIND; p99 HTTP duration minus DDNS round-trip under ~20 ms.

## Key packages

| Package | Role |
|---------|------|
| `internal/config` | koanf YAML + env; secrets via inline / `secret_file` / `${ENV}` |
| `internal/dnsx` | Client, pool, cache, IXFR, nsupdate, notify, reverse, Zone |
| `internal/store` | bun + goose (SQLite & Postgres), outbox, retention |
| `internal/scheduler` | claim/execute/revert loop |
| `internal/notifications` | formatters + durable outbox worker |
| `internal/live` | WebSocket hub |
| `internal/httpapi` | Huma routes, middleware, opaque cursors, SPA |
| `internal/catalog` | RFC 9432 catalog zone indexer |
| `internal/provision` | Optional zone create/delete (BIND rndc today) |

## Zone provisioning and BIND coupling

Day-to-day zone *contents* stay server-agnostic: AXFR / IXFR / DDNS / NOTIFY /
TSIG against whichever hidden primary is configured. Optional **zone
create/delete** is the exception. `POST /v1/zones` and `DELETE /v1/zones/{zone}`
go through `internal/provision`, which today talks **rndc** (`addzone` /
`delzone`), writes a BIND seed zone file (`zone_seed.mode: shared_dir` on 9.20),
and optionally adds/removes an RFC 9432 catalog PTR via DDNS.

That is a BIND-primary coupling, not a core DNS one. Catalog membership, cache
`LoadZone`, and the HTTP/CLI surface are already behind `httpapi.ZoneProvisioner`
and do not need rndc. A Knot / NSD / PowerDNS primary should be a second
provision backend (control-channel + seed strategy) with the same
create/delete/status contract — keep BIND-specific config and `rndc-go` inside
that implementation.

## API differences from the retired Python service

- Errors are RFC 9457 `application/problem+json` (with `rcode` extensions)
- List endpoints always return paginated envelopes
- Cursors are opaque base64url JSON
- Production binary embeds the SPA; `/ready` is the probe for orchestration
