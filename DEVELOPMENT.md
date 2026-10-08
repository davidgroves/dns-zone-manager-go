# Development (Go)

Work on this tree **inside the Dev Container**. The workspace is
`.devcontainer/`: a Compose project with the `dev` service (Go, Node, DNS
tools, Docker socket), sibling **BIND** (`bind:15353`), **PostgreSQL**
(tmpfs; schema is created when the API starts), optional **LGTM**
(Grafana / Loki / Prometheus / Tempo), an optional **Traefik +
oauth2-proxy + Entra emulator** OIDC front door, and an optional
**webhook-mocker** Teams/Slack incoming-webhook sink.

In Cursor or VS Code: clone the repo, install the Dev Containers extension,
then **Reopen in Container**. First create runs `npm install`, `go mod
download`, and pre-commit. Auth is on: API key `dev` on `:8000`, OIDC via
Traefik on `:8080` when the ENTRA/TRAEFIK tasks are running.

## The dev image is prebuilt (pulled, not built)

The `dev` service uses a prebuilt image published to GHCR
(`ghcr.io/davidgroves/dns-zone-manager-devcontainer:latest`), not a local
`build:`. Reopening in the container **pulls** that image rather than building
one on your machine. This is what makes clone-and-go work the same on macOS
(Docker Desktop) and Linux: building a complex image locally is where the
cross-platform friction lives (Docker Desktop's apt GPG verification, VM clock
drift, and BuildKit `fs.read` entitlement checks all only bite during a local
build). The image is multi-arch, so Apple Silicon (`linux/arm64`) and
Intel/Linux (`linux/amd64`) both get a native image.

### Changing the dev image

Edit `.devcontainer/Dockerfile`, then publish a new image one of two ways:

- **CI (preferred):** push the change to `main`. The
  `.github/workflows/devcontainer-image.yml` workflow rebuilds and pushes
  `:latest` (and a commit-SHA tag). It also runs on `workflow_dispatch`.
- **Manually:** `docker login ghcr.io` then `make devcontainer-image` (builds
  both arches via buildx and pushes `:latest`).

The base is Debian Trixie (Debian 13): Bookworm shipped `apt 2.6.1`, which has a
GPG-signature verification bug; Trixie's `apt 3.0.3` resolves it.

The API does **not** start with the container. Use **Tasks** after attach:

- **Terminal → Run Task…** (or Command Palette → **Tasks: Run Task**)
- **Run All (Minimal)** — default build task (`Ctrl+Shift+B` / **Run Build
  Task**). Starts **BACKEND**, **BIND** log follow, and **POSTGRES** log
  follow. BACKEND builds the SPA into `dist/` then
  `go run … serve --config .devcontainer/config.devcontainer.yaml --ui-dir dist`.
- **Run All (Complete)** — the same, plus **ZONE CHURN**, **LGTM**, **ENTRA**
  (local Entra ID emulator), **TRAEFIK** (oauth2-proxy + Traefik), and
  **WEBHOOKS** (Teams/Slack webhook-mocker).

Open **http://localhost:8000** for the API and the UI (API key `dev`). With
Complete, Grafana is **http://localhost:3000**, the OIDC front door is
**http://localhost:8080**, and webhook-mocker is **http://localhost:5080**.
Stop tasks with **Terminal: Kill All Terminals**.

BIND and Postgres are already Compose services; those tasks mainly attach to
logs. BACKEND is the process you restart after Go changes.

## OIDC front door (Traefik + Entra emulator)

Mirrors a production Traefik → oauth2-proxy → Entra ID deployment. The local
IdP is [entra-emulator](https://github.com/calvinchengx/entra-emulator); the
fixture in [`.devcontainer/entra/`](.devcontainer/entra/) seeds users and apps.

| URL | What |
|---|---|
| http://localhost:8000 | API + SPA (API key `dev` / `demo-api-key-12345`) |
| http://localhost:8080 | Same app via Traefik (OIDC: “Sign in with Entra ID” then IdP) |
| http://localhost:8081 | Traefik dashboard |
| http://localhost:8444 | Entra emulator (OIDC issuer; dark sign-in via themeproxy) |
| http://localhost:5080 | webhook-mocker UI (Teams Adaptive Cards from DNS changes) |

Dummy users: `user1@dns-zone-manager.test` / `pass1` (also user2/pass2, user3/pass3).

## Teams webhooks (webhook-mocker)

Dev config enables outbound `type: teams` notifications to a local
[webhook-mocker](https://github.com/davidgroves/webhook-mocker) sink (fixture in
[`.devcontainer/webhook-mocker/`](.devcontainer/webhook-mocker/)). Start the
**WEBHOOKS** task (or **Run All (Complete)**), make a DNS change, then open
**http://localhost:5080** and the **dns** virtual channel.

```text
POST http://localhost:5080/teams/workflows/dns-dev   ← teams-dns target
```

`webhook-mocker` shares the `dev` network namespace (like Entra), so the API and
browser both use `localhost:5080`. Publishing that port on `dev` needs a
**one-time Rebuild Container** after pulling this change if `:5080` is not
already mapped.

```bash
# After ENTRA + TRAEFIK + BACKEND are up:
dns-cli auth login --url http://localhost:8080
# open the printed verification URL, approve as user1 / pass1
dns-cli --url http://localhost:8080 list zones
dns-cli auth status
```

`entra`, `oauth2-proxy`, and `traefik` share the `dev` container’s network
namespace so the issuer `http://localhost:8444/...` is the same URL for the
host browser, `dns-cli`, and oauth2-proxy. Publishing those ports on `dev`
requires a **one-time Rebuild Container** after pulling this change.

To point the same stack at real Entra ID later: set oauth2-proxy
`OAUTH2_PROXY_PROVIDER=entra-id` and a real issuer/client secret, and run
`dns-cli auth login --issuer https://login.microsoftonline.com/<tenant>/v2.0
--client-id <app> --scope 'api://…/access_as_user offline_access'`.

## Frontend live reload (Vite)

BACKEND’s `--ui-dir dist` is a **static** Vite production build. Editing
`frontend/` does not update http://localhost:8000 until you run **Build SPA**
again (or restart BACKEND, which depends on that build).

For HMR, start **Vite (optional)** (`npm run dev`). That serves the SPA at
**http://localhost:5173** and proxies `/v1`, WebSocket `/v1/live`, `/health`,
and `/ui/…` to the API on :8000. Keep BACKEND running. Use :5173 in the
browser while changing Alpine/TS/CSS; use :8000 when you care about the
embedded-SPA path the production binary uses.

Playwright E2E defaults to :5173, so Vite must be up for those tests.

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

## Forked miekg/dns

`go.mod` replaces `github.com/miekg/dns` with
[davidgroves/dns](https://github.com/davidgroves/dns) branch
`fix/tcp-tsig-response-mac` (commit `03be1a43`).

miekg `Conn.WriteMsg` feeds the previous message's MAC into the next
signature. BIND treats each UPDATE on a kept-open TCP connection as a new
transaction and verifies it with an empty prior MAC, so the second update
on a pooled connection was `BADSIG`. The fork signs each request with an
empty prior MAC. Multi-message answers still chain inside `Transfer`.

Drop the `replace` once that behavior is in an upstream release and this
module requires that version.

## Migrations

Schema is applied at store open (goose). Point `database` at an empty SQLite
file or Postgres DSN; no manual step when `auto_migrate: true`.
