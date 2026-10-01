# Perf: smoke-large-zone-load

- Started: `2026-09-30T11:12:06.600488+00:00`
- Finished: `2026-09-30T11:12:18.339954+00:00`
- Config: `{"api_base": "http://127.0.0.1:8000", "bind": "bind:15353", "scenario_file": "smoke-large-zone-load", "zone": "perf5m.test"}`

## Load steps

| Step | Target rps | Achieved rps | Count | Errors | p50 ms | p95 ms | p99 ms | max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold-reads | 0.0 | 0.0 | 0 | 0 | - | - | - | - |

### Readers (cold-reads)

| Op | Count | Errors | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| first_page | 1 | 0 | 1184.4 | 1184.4 | 1184.4 |
| deep_page | 1 | 0 | 725.1 | 725.1 | 725.1 |
| search | 1 | 0 | 428.7 | 428.7 | 428.7 |
| zone_list | 1 | 0 | 33.7 | 33.7 | 33.7 |
| export | 1 | 0 | 1029.2 | 1029.2 | 1029.2 |

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
    "mem_usage": "238.8MiB / 93.6GiB",
    "mem_perc": "0.25%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "4.00%",
    "mem_usage": "868.3MiB / 93.6GiB",
    "mem_perc": "0.91%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "5.91%",
    "mem_usage": "3.627GiB / 93.6GiB",
    "mem_perc": "3.87%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "159.9MiB / 93.6GiB",
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

Serial lag: `{'bind_serial': 1790766644, 'app_serial': 1790766644, 'lag_seconds': 0.04883109198999591, 'timed_out': False, 'timeout_seconds': 30.0}`

### after

Docker stats:

```json
{
  "dns-zone-manager_devcontainer-bind-1": {
    "cpu_perc": "2.77%",
    "mem_usage": "240MiB / 93.6GiB",
    "mem_perc": "0.25%"
  },
  "dns-zone-manager_devcontainer-lgtm-1": {
    "cpu_perc": "4.17%",
    "mem_usage": "877MiB / 93.6GiB",
    "mem_perc": "0.92%"
  },
  "dns-zone-manager_devcontainer-dev-1": {
    "cpu_perc": "42.53%",
    "mem_usage": "3.653GiB / 93.6GiB",
    "mem_perc": "3.90%"
  },
  "dns-zone-manager_devcontainer-postgres-1": {
    "cpu_perc": "0.23%",
    "mem_usage": "160.4MiB / 93.6GiB",
    "mem_perc": "0.17%"
  },
  "dnssec-auditor_devcontainer-bind-1": {
    "cpu_perc": "0.00%",
    "mem_usage": "191MiB / 93.6GiB",
    "mem_perc": "0.20%"
  },
  "dnssec-auditor-bind": {
    "cpu_perc": "0.00%",
    "mem_usage": "237.9MiB / 93.6GiB",
    "mem_perc": "0.25%"
  }
}
```

Serial lag: `{'bind_serial': 1790766644, 'app_serial': 1790766644, 'lag_seconds': 0.04598640299809631, 'timed_out': False, 'timeout_seconds': 30.0}`

## Notes

- ephemeral smoke

