#!/usr/bin/env bash
# Remove compose containers whose NetworkID no longer exists.
#
# Docker Compose keeps NetworkID on stopped containers. After
# `docker network prune` (or a recreate that assigns a new ID to the same
# name), `docker compose up` tries to start those containers and fails with:
#   network <id> not found
# Deleting the stale containers lets Compose recreate them on the live network.
set -euo pipefail

PROJECT="${COMPOSE_PROJECT_NAME:-dns-zone-manager-go_devcontainer}"

ids="$(docker ps -aq --filter "label=com.docker.compose.project=${PROJECT}" 2>/dev/null || true)"
if [[ -z "${ids}" ]]; then
  exit 0
fi

removed=0
while IFS= read -r id; do
  [[ -z "${id}" ]] && continue
  # One NetworkID per attached network (usually just "devnet").
  mapfile -t nids < <(docker inspect -f '{{range .NetworkSettings.Networks}}{{.NetworkID}}{{"\n"}}{{end}}' "${id}" 2>/dev/null || true)
  for nid in "${nids[@]:-}"; do
    [[ -z "${nid}" ]] && continue
    if ! docker network inspect "${nid}" >/dev/null 2>&1; then
      name="$(docker inspect -f '{{.Name}}' "${id}" 2>/dev/null | sed 's#^/##')"
      echo "Removing stale container ${name:-$id} (missing network ${nid})"
      docker rm -f "${id}" >/dev/null
      removed=$((removed + 1))
      break
    fi
  done
done <<< "${ids}"

if [[ "${removed}" -gt 0 ]]; then
  echo "Removed ${removed} container(s) with stale network references."
fi
