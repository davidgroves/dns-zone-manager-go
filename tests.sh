#!/usr/bin/env bash
# Run all tests (backend and frontend)
#
# Usage:
#   ./tests.sh              Run unit tests only (Go + frontend)
#   ./tests.sh --all        Run all tests (unit + integration + playwright if available)
#   ./tests.sh --integration  Run only integration tests
#
# Integration tests require Docker.
# Playwright E2E requires a running API + Vite (or embedded SPA) stack.
#
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RUN_INTEGRATION=false
RUN_UNIT=true

for arg in "$@"; do
    case $arg in
        --all)
            RUN_INTEGRATION=true
            ;;
        --integration)
            RUN_INTEGRATION=true
            RUN_UNIT=false
            ;;
    esac
done

if $RUN_UNIT; then
    echo "=== Go Unit Tests ==="
    go test ./...

    echo ""
    echo "=== Frontend Unit Tests ==="
    npm run test
fi

if $RUN_INTEGRATION; then
    echo ""
    echo "=== Go Integration Tests (requires Docker) ==="
    go test -tags=integration ./tests/integration/... -v

    echo ""
    echo "=== Frontend E2E Tests (requires running stack) ==="

    if ! curl -s --max-time 2 http://localhost:8000/health > /dev/null 2>&1; then
        echo "SKIP: API not running on http://localhost:8000 — skipping Playwright"
    elif ! curl -s --max-time 2 http://localhost:5173 > /dev/null 2>&1; then
        echo "SKIP: Vite not running on http://localhost:5173 — skipping Playwright"
    else
        echo "Dev servers detected, running E2E tests..."
        npm run test:e2e
    fi
fi

echo ""
echo "All tests passed!"
