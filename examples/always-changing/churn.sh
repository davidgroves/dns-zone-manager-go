#!/bin/sh
# Continuously mutate always-changing.example via direct DDNS and the REST API.
# Each tick uses exactly one mode, cycling:
#   DDNS → API → BOTH(ddns) → DDNS → API → BOTH(api) → …
#   DDNS:       update ddns-* via nsupdate
#   API:        update api-* via PUT /v1/.../rrsets
#   BOTH(ddns): update both via nsupdate
#   BOTH(api):  update both via PUT /v1/.../rrsets
set -eu

ZONE="${ZONE:-always-changing.example.}"
BIND_HOST="${BIND_HOST:-bind}"
BIND_PORT="${BIND_PORT:-15353}"
API_BASE="${API_BASE:-http://dev:8000}"
API_KEY="${API_KEY:-}"
INTERVAL_SEC="${INTERVAL_SEC:-5}"
TSIG_NAME="${TSIG_NAME:-dns-api-key}"
TSIG_SECRET="${TSIG_SECRET:-K8vC2mP9nQ4rT6wX1yB3fG5hJ7kL0mN2pR4sU6vW8xY=}"
TSIG_ALG="${TSIG_ALG:-hmac-sha256}"
TTL="${TTL:-60}"

case "$API_BASE" in
  */) API_BASE="${API_BASE%/}" ;;
esac

ZONE_PATH="$ZONE"
case "$ZONE_PATH" in
  *.) ZONE_PATH="${ZONE_PATH%.}" ;;
esac

log() {
  echo "[zone-churn] $*"
}

wait_for_bind() {
  i=0
  while [ "$i" -lt 90 ]; do
    if dig @"${BIND_HOST}" -p "${BIND_PORT}" "${ZONE}" SOA +time=2 +tries=1 >/dev/null 2>&1; then
      log "BIND ready at ${BIND_HOST}:${BIND_PORT}"
      return 0
    fi
    i=$((i + 1))
    sleep 2
  done
  log "ERROR: BIND not ready after waiting"
  return 1
}

wait_for_api() {
  i=0
  while [ "$i" -lt 90 ]; do
    if curl -sf --max-time 2 "${API_BASE}/health" >/dev/null 2>&1; then
      log "API ready at ${API_BASE}"
      return 0
    fi
    i=$((i + 1))
    sleep 2
  done
  log "ERROR: API not ready after waiting at ${API_BASE}"
  return 1
}

ddns_set() {
  name="$1"
  addr="$2"
  nsupdate -y "${TSIG_ALG}:${TSIG_NAME}:${TSIG_SECRET}" <<EOF
server ${BIND_HOST} ${BIND_PORT}
zone ${ZONE}
update delete ${name}.${ZONE} A
update add ${name}.${ZONE} ${TTL} A ${addr}
send
EOF
}

ddns_delete() {
  name="$1"
  nsupdate -y "${TSIG_ALG}:${TSIG_NAME}:${TSIG_SECRET}" <<EOF
server ${BIND_HOST} ${BIND_PORT}
zone ${ZONE}
update delete ${name}.${ZONE} A
send
EOF
}

# Drop legacy both-1/2/3 left over from older seeds; ensure single `both` exists.
ensure_both_seed() {
  for i in 1 2 3; do
    ddns_delete "both-${i}" || true
  done
  if ! ddns_set "both" "203.0.113.16"; then
    log "warning: failed to seed both"
    return 1
  fi
  refresh_zone || log "warning: zone refresh after both seed failed"
}

api_set() {
  name="$1"
  addr="$2"
  body="{\"name\":\"${name}\",\"ttl\":${TTL},\"type\":\"A\",\"rdclass\":\"IN\",\"records\":[\"${addr}\"]}"
  if [ -n "$API_KEY" ]; then
    curl -sf --max-time 10 -X PUT \
      "${API_BASE}/v1/zones/${ZONE_PATH}/rrsets" \
      -H "Content-Type: application/json" \
      -H "X-API-Key: ${API_KEY}" \
      -d "$body" \
      >/dev/null
  else
    curl -sf --max-time 10 -X PUT \
      "${API_BASE}/v1/zones/${ZONE_PATH}/rrsets" \
      -H "Content-Type: application/json" \
      -d "$body" \
      >/dev/null
  fi
}

# Soft-refresh zone cache so API PUT prerequisites match BIND after external DDNS.
# Do not DELETE the cache: that races the NOTIFY listener (PeekZone nil → dropped
# live broadcast) and is why both rows used to miss UI flashes.
refresh_zone() {
  if [ -n "$API_KEY" ]; then
    curl -sf --max-time 30 -X POST \
      "${API_BASE}/v1/zones/${ZONE_PATH}/refresh" \
      -H "X-API-Key: ${API_KEY}" \
      >/dev/null
  else
    curl -sf --max-time 30 -X POST \
      "${API_BASE}/v1/zones/${ZONE_PATH}/refresh" \
      >/dev/null
  fi
}

log "starting ZONE=${ZONE} BIND=${BIND_HOST}:${BIND_PORT} API=${API_BASE} interval=${INTERVAL_SEC}s cycle=DDNS,API,BOTH(ddns),DDNS,API,BOTH(api)"
wait_for_bind
wait_for_api
ensure_both_seed
log "seeded both=203.0.113.16 (removed legacy both-1/2/3 if present)"

n=0
while true; do
  n=$((n + 1))
  octet=$(( (n % 200) + 20 ))
  addr="203.0.113.${octet}"
  # Cycle: DDNS → API → BOTH(ddns) → DDNS → API → BOTH(api)
  phase=$(( (n - 1) % 6 ))

  status=ok
  case "$phase" in
    0|3)
      mode=DDNS
      for i in 1 2 3; do
        if ! ddns_set "ddns-${i}" "$addr"; then
          status=fail
        fi
      done
      ;;
    1|4)
      mode=API
      if ! refresh_zone; then
        log "tick=${n} warning: zone refresh failed"
      fi
      for i in 1 2 3; do
        if ! api_set "api-${i}" "$addr"; then
          status=fail
        fi
      done
      ;;
    2)
      mode='BOTH(ddns)'
      if ! ddns_set "both" "$addr"; then
        status=fail
      fi
      ;;
    5)
      mode='BOTH(api)'
      if ! refresh_zone; then
        log "tick=${n} warning: zone refresh failed"
      fi
      if ! api_set "both" "$addr"; then
        status=fail
      fi
      ;;
  esac

  log "tick=${n} mode=${mode} addr=${addr} status=${status}"
  sleep "$INTERVAL_SEC"
done
