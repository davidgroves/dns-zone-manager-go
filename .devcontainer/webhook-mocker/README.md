# Local webhook-mocker fixture

Committed config for [webhook-mocker](https://github.com/davidgroves/webhook-mocker):
a Slack / Microsoft Teams incoming-webhook mocker with a live virtual-channel UI.

| File | Purpose |
|---|---|
| `webhook-mocker.yaml` | Canonical fixture (also inlined into `docker-compose.yml` configs) |

Start via the **WEBHOOKS** VS Code task (also included in **Run All (Complete)**).

| URL | What |
|---|---|
| http://localhost:5080 | Virtual-channel UI |
| `POST http://localhost:5080/teams/workflows/dns-dev` | Teams Workflows sink (Adaptive Card) |

The mocker shares the `dev` container’s network namespace (like the Entra
emulator), so the API and the host browser both use `localhost:5080`.

**Do not expose this on the public internet** — captured bodies and secrets are
stored unredacted.
