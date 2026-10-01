# Perf: rapid-api-writes

- Started: `2026-09-30T11:20:29.423703+00:00`
- Finished: `2026-09-30T11:26:23.373166+00:00`
- Config: `{"api_base": "http://127.0.0.1:8000", "bind": "bind:15353", "scenario_file": "rapid-api-writes", "zone": "perf5m.test"}`

## Load steps

| Step | Target rps | Achieved rps | Count | Errors | p50 ms | p95 ms | p99 ms | max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| api@50 | 50.0 | 50.0 | 3000 | 0 | 10.2 | 12.7 | 23.9 | 32.5 |
| api@100 | 100.0 | 100.0 | 6000 | 0 | 11.3 | 28.9 | 49.1 | 68.9 |
| api@250 | 250.0 | 183.6 | 14564 | 0 | 8986.3 | 18305.9 | 19151.0 | 19387.9 |
| api@500 | 500.0 | 192.9 | 28208 | 0 | 41675.1 | 82566.1 | 85740.5 | 86275.1 |

## Metrics delta

| Metric | Delta |
| --- | ---: |
| `dns_zone_manager_ddns_updates_successful_total` | 51772 |
| `dns_zone_manager_rrset_replaces_total` | 51772 |
| `dns_zone_manager_rrset_replaces_total{zone="perf5m.test."}` | 51772 |
| `dns_zone_manager_webhook_autorecorded_changes_total` | 9817 |
| `dns_zone_manager_webhook_autorecorded_changes_total{outcome="recorded"}` | 9817 |
| `dns_zone_manager_webhook_queue_dropped_total` | 41955 |

## Probes

### before

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "246.9MiB / 93.6GiB",
    "mem_perc": "0.26%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "3.96%",
    "mem_usage": "901.4MiB / 93.6GiB",
    "mem_perc": "0.94%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "51.81%",
    "mem_usage": "3.678GiB / 93.6GiB",
    "mem_perc": "3.93%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "0.01%",
    "mem_usage": "174.5MiB / 93.6GiB",
    "mem_perc": "0.18%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "2.58%",
    "mem_usage": "238.7MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790768990, 'app_serial': 1790768990, 'lag_seconds': 0.047672790999058634, 'timed_out': False, 'timeout_seconds': 30.0}`

### after

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "246.1MiB / 93.6GiB",
    "mem_perc": "0.26%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "4.54%",
    "mem_usage": "955.6MiB / 93.6GiB",
    "mem_perc": "1.00%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "17.09%",
    "mem_usage": "3.709GiB / 93.6GiB",
    "mem_perc": "3.96%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "4.83%",
    "mem_usage": "234.1MiB / 93.6GiB",
    "mem_perc": "0.24%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.6MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790768990, 'app_serial': 1790768990, 'lag_seconds': 0.05152726599771995, 'timed_out': False, 'timeout_seconds': 30.0}`

## Notes

- Each write opens a new TCP connection to BIND (no connection reuse today).
- DNS I/O is synchronous on the FastAPI event loop.

