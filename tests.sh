#!/usr/bin/env bash
# Run all tests (backend and frontend) via cmd/dns-tests.
#
# Usage:
#   ./tests.sh                         Unit tests only (Go + frontend)
#   ./tests.sh --all                   Unit + integration + Playwright
#   ./tests.sh --integration           Go integration tests only
#   ./tests.sh --report report.pdf     Also write a PDF summary
#
# Integration tests require Docker.
# Playwright E2E requires a running API + Vite (or embedded SPA) stack.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

exec go run ./cmd/dns-tests "$@"
