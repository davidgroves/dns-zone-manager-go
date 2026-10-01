# Perf: rapid-api-writes

- Started: `2026-09-30T11:50:48.003415+00:00`
- Finished: `2026-09-30T11:56:39.306242+00:00`
- Config: `{"api_base": "http://127.0.0.1:8000", "bind": "bind:15353", "scenario_file": "rapid-api-writes", "zone": "perf5m.test"}`

## Load steps

| Step | Target rps | Achieved rps | Count | Errors | p50 ms | p95 ms | p99 ms | max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| api@50 | 50.0 | 50.0 | 3000 | 0 | 11.0 | 14.0 | 31.9 | 414.5 |
| api@100 | 100.0 | 100.0 | 6000 | 0 | 17.0 | 40.9 | 61.8 | 82.6 |
| api@250 | 250.0 | 199.4 | 14866 | 0 | 6538.5 | 14500.5 | 14673.9 | 14766.0 |
| api@500 | 500.0 | 195.4 | 28995 | 0 | 44828.1 | 85025.3 | 87683.9 | 88389.5 |

## Metrics delta

| Metric | Delta |
| --- | ---: |
| `dns_zone_manager_ddns_updates_successful_total` | 52861 |
| `dns_zone_manager_rrset_replaces_total` | 52861 |
| `dns_zone_manager_rrset_replaces_total{zone="perf5m.test."}` | 52861 |
| `dns_zone_manager_webhook_autorecorded_changes_total` | 9925 |
| `dns_zone_manager_webhook_autorecorded_changes_total{outcome="recorded"}` | 9925 |
| `dns_zone_manager_webhook_queue_dropped_total` | 42936 |

## Probes

### before

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "2.36%",
    "mem_usage": "249.5MiB / 93.6GiB",
    "mem_perc": "0.26%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "7.53%",
    "mem_usage": "1001MiB / 93.6GiB",
    "mem_perc": "1.04%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "53.65%",
    "mem_usage": "3.688GiB / 93.6GiB",
    "mem_perc": "3.94%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "0.16%",
    "mem_usage": "262.8MiB / 93.6GiB",
    "mem_perc": "0.27%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.5MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790768994, 'app_serial': 1790768994, 'lag_seconds': 0.04526106399134733, 'timed_out': False, 'timeout_seconds': 30.0}`

### after

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "246.5MiB / 93.6GiB",
    "mem_perc": "0.26%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "7.69%",
    "mem_usage": "1.006GiB / 93.6GiB",
    "mem_perc": "1.08%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "12.15%",
    "mem_usage": "3.755GiB / 93.6GiB",
    "mem_perc": "4.01%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "5.44%",
    "mem_usage": "288.6MiB / 93.6GiB",
    "mem_perc": "0.30%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "2.17%",
    "mem_usage": "238.5MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790768994, 'app_serial': 1790768994, 'lag_seconds': 0.04441519199463073, 'timed_out': False, 'timeout_seconds': 30.0}`

## Notes

- DDNS updates are offloaded to a thread pool (event loop no longer blocked per update).
- Each write still opens a new TCP connection to BIND (no connection reuse yet).

