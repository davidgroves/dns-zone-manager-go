# Performance

Opt-in harness under `perf/` (Python tooling, unchanged from the original
project). The Go backend is the system under test.

## Goals

Python capped near ~200 REST writes/s. Target for this codebase: BIND is the
limit. Measure with:

```bash
./perf.sh run rapid-api-writes
./perf.sh run rapid-ddns-writes   # baseline: BIND alone
./perf.sh run mixed-read-write
./perf.sh run websocket-fanout
./perf.sh run soak
./perf.sh run many-zones-breadth
./perf.sh run scheduler-throughput
```

Compare REST achieved rps to direct-DDNS rps on the same BIND. Watch:

- `dns_zone_manager_ddns_roundtrip_seconds`
- `dns_zone_manager_http_request_duration_seconds`

Application overhead ≈ HTTP histogram − DDNS histogram.

## Design levers already in the Go port

1. Persistent TCP pool to BIND (`dns.pool_size`, default 8)
2. Sorted zone index + maintained RRset counts (no full-sort pagination)
3. Debounced post-write SOA serial refresh
4. Per-subscriber WebSocket outbox channels
5. Durable webhook outbox drained in batches (off the request path)

## Baseline table

Fill after serious runs on a known host:

| Scenario | Key metric | Result |
| --- | --- | --- |
| rapid-ddns-writes | achieved rps @ 500 target | |
| rapid-api-writes | achieved rps @ 500 target | |
| rapid-api-writes | p99 (http − ddns) ms | |
| large-zone-load 5m | AXFR refresh (s) | |
| websocket-fanout | messages @ 500 subs | |
