# Development (Go)

Work on this tree **inside the Dev Container**. The workspace is
`.devcontainer/`: a Compose project with the `dev` service (Go, Node, DNS
tools, Docker socket), sibling **BIND** (`bind:15353`), **PostgreSQL**
(tmpfs; schema is created when the API starts), and optional **LGTM**
(Grafana / Loki / Prometheus / Tempo).

In Cursor or VS Code: clone the repo, install the Dev Containers extension,
then **Reopen in Container**. First create runs `npm install`, `go mod
download`, and pre-commit. Auth is off in this config (anonymous admin).

## macOS (Docker Desktop)

The devcontainer uses Debian Trixie (Debian 13) as its base. Bookworm (Debian 12)
shipped `apt 2.6.1` which has a bug verifying GPG signatures in Docker Desktop's
network environment, causing `apt-get update` to fail with
`At least one invalid signature was encountered` on every repository. Trixie
ships `apt 3.0.3` which resolves this.

The `initializeCommand` (`initialize.sh`) also syncs the Docker Desktop VM clock
before each container start (`docker run --privileged alpine hwclock -s`), as
clock drift after sleep/wake can cause unrelated GPG timestamp failures.

The API does **not** start with the container. Use **Tasks** after attach:

- **Terminal → Run Task…** (or Command Palette → **Tasks: Run Task**)
- **Run All (Minimal)** — default build task (`Ctrl+Shift+B` / **Run Build
  Task**). Starts **BACKEND**, **BIND** log follow, and **POSTGRES** log
  follow. BACKEND builds the SPA into `dist/` then
  `go run … serve --config .devcontainer/config.devcontainer.yaml --ui-dir dist`.
- **Run All (Complete)** — the same, plus **ZONE CHURN** (DDNS against
  `always-changing.example`) and **LGTM** (`docker compose up` Grafana).

Open **http://localhost:8000** for the API and the UI the binary is serving.
With Complete, Grafana is **http://localhost:3000**. Stop tasks with
**Terminal: Kill All Terminals**.

BIND and Postgres are already Compose services; those tasks mainly attach to
logs. BACKEND is the process you restart after Go changes.

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
`fix/tcp-tsig-response-mac` (commit `537ba7e9`).

miekg v1.1.63 does not chain the TSIG MAC across messages on one TCP
connection. BIND requires that chain, so the second signed update on a
pooled connection was `BADSIG`. The pool treated that as a broken socket
and closed it, so each connection carried one successful DDNS update and
then one failure. The retry of the failed update had already had its TSIG
record removed by signing, so it was sent unsigned and BIND refused it.
Half of a sustained run failed.

Drop the `replace` once the fix is in an upstream release and this module
requires that version.

## Migrations

Schema is applied at store open (goose). Point `database` at an empty SQLite
file or Postgres DSN; no manual step when `auto_migrate: true`.
