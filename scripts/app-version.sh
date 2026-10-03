#!/usr/bin/env bash
# Print the application version: exact git tag, or nearest-tag+short-sha, or v0.0.0+unknown.
set -euo pipefail
if exact="$(git describe --tags --exact-match HEAD 2>/dev/null)" && [ -n "$exact" ]; then
	printf '%s\n' "$exact"
	exit 0
fi
tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
if [ -n "${tag}" ] && [ -n "${sha}" ]; then
	printf '%s\n' "${tag}+${sha}"
	exit 0
fi
printf '%s\n' "v0.0.0+unknown"
