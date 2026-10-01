# Perf: smoke-large-zone-load

- Started: `2026-09-30T11:11:21.408788+00:00`
- Finished: `2026-09-30T11:11:39.530097+00:00`
- Config: `{"api_base": "http://127.0.0.1:8000", "bind": "bind:15353", "scenario_file": "smoke-large-zone-load", "zone": "perf5m.test"}`

## Load steps

| Step | Target rps | Achieved rps | Count | Errors | p50 ms | p95 ms | p99 ms | max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold-reads | 0.0 | 0.0 | 0 | 0 | - | - | - | - |

### Readers (cold-reads)

| Op | Count | Errors | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| first_page | 1 | 0 | 1080.3 | 1080.3 | 1080.3 |
| deep_page | 1 | 0 | 745.8 | 745.8 | 745.8 |
| search | 1 | 1 | 3.8 | 3.8 | 3.8 |
| zone_list | 1 | 0 | 24.9 | 24.9 | 24.9 |
| export | 1 | 0 | 998.5 | 998.5 | 998.5 |

## Metrics delta

| Metric | Delta |
| --- | ---: |

## Probes

### before

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.7MiB / 93.6GiB",
    "mem_perc": "0.25%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "3.79%",
    "mem_usage": "864.7MiB / 93.6GiB",
    "mem_perc": "0.90%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "11.50%",
    "mem_usage": "3.538GiB / 93.6GiB",
    "mem_perc": "3.78%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "0.07%",
    "mem_usage": "160.7MiB / 93.6GiB",
    "mem_perc": "0.17%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.3MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790766644, 'app_serial': 1790766644, 'lag_seconds': 6.975345643993933, 'timed_out': False, 'timeout_seconds': 30.0}`

### after

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.8MiB / 93.6GiB",
    "mem_perc": "0.25%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "9.83%",
    "mem_usage": "865MiB / 93.6GiB",
    "mem_perc": "0.90%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "5.87%",
    "mem_usage": "3.651GiB / 93.6GiB",
    "mem_perc": "3.90%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "160.5MiB / 93.6GiB",
    "mem_perc": "0.17%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.3MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790766644, 'app_serial': 1790766644, 'lag_seconds': 0.04550101100176107, 'timed_out': False, 'timeout_seconds': 30.0}`

## Notes

- ephemeral smoke

