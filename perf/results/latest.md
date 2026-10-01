# Perf: writes-many-zones

- Started: `2026-10-01T14:40:35.869529404Z`
- Finished: `2026-10-01T14:41:13.794095288Z`
- Config: `{"api_base":"http://127.0.0.1:8000","bind":"bind:15353","zone":"alpha.test"}`

## Load steps

| Step | Target rps | Achieved rps | Count | Errors | p50 ms | p95 ms | p99 ms | max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| many-zones@500 | 500.0 | 500.0 | 15000 | 0 | 4.3 | 6.9 | 8.8 | 18.7 |

## Metrics delta

| Metric | Delta |
| --- | ---: |
| `dns_zone_manager_ddns_updates_successful_total` | 15000 |
| `dns_zone_manager_rrset_adds_total` | 500 |
| `dns_zone_manager_rrset_replaces_total` | 14500 |
| `dns_zone_manager_zone_transfers_total` | 1 |
| `dns_zone_manager_zone_transfers_total{method="axfr"}` | 1 |

## Probes

### before

Docker stats:

```json
{
  "dns-zone-manager-go_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_perc": "1.86%",
    "mem_usage": "1.741GiB / 93.6GiB"
  },
  "dns-zone-manager-go_devcontainer-dev-1": {
    "cpu_perc": "2.76%",
    "mem_perc": "1.53%",
    "mem_usage": "1.434GiB / 93.6GiB"
  },
  "dns-zone-manager-go_devcontainer-lgtm-1": {
    "cpu_perc": "9.51%",
    "mem_perc": "0.18%",
    "mem_usage": "171.5MiB / 93.6GiB"
  },
  "dns-zone-manager-go_devcontainer-postgres-1": {
    "cpu_perc": "0.00%",
    "mem_perc": "0.03%",
    "mem_usage": "25.58MiB / 93.6GiB"
  }
}
```

Serial sample: `{"app_serial":1790864307,"bind_serial":1790864307,"timed_out":false}`

### after

Docker stats:

```json
{
  "dns-zone-manager-go_devcontainer-bind-1": {
    "cpu_perc": "2.29%",
    "mem_perc": "1.87%",
    "mem_usage": "1.746GiB / 93.6GiB"
  },
  "dns-zone-manager-go_devcontainer-dev-1": {
    "cpu_perc": "3.65%",
    "mem_perc": "1.61%",
    "mem_usage": "1.505GiB / 93.6GiB"
  },
  "dns-zone-manager-go_devcontainer-lgtm-1": {
    "cpu_perc": "7.02%",
    "mem_perc": "0.18%",
    "mem_usage": "170.1MiB / 93.6GiB"
  },
  "dns-zone-manager-go_devcontainer-postgres-1": {
    "cpu_perc": "0.21%",
    "mem_perc": "0.03%",
    "mem_usage": "26.1MiB / 93.6GiB"
  }
}
```

Serial sample: `{"app_serial":1790865658,"bind_serial":1790865658,"timed_out":false}`

## Notes

- Same rate and record shape as writes-one-zone. Each change goes to the next zone.

