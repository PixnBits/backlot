#!/usr/bin/env bash
# Phase 1 compose control-plane sit: health 200, lease 503, no /dev/kvm on desk/router.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE=(docker compose -f "$ROOT/deploy/compose/docker-compose.yml")
PROJECT="${COMPOSE_PROJECT_NAME:-backlot-cp}"
ROUTER_PORT="${BACKLOT_ROUTER_HOST_PORT:-18080}"
DESK_PORT="${BACKLOT_DESK_HOST_PORT:-18090}"
export BACKLOT_ROUTER_HOST_PORT="$ROUTER_PORT"
export BACKLOT_DESK_HOST_PORT="$DESK_PORT"

cleanup() {
  "${COMPOSE[@]}" -p "$PROJECT" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> build + up desk router (no kvm profile) host ports router=$ROUTER_PORT desk=$DESK_PORT"
cleanup
"${COMPOSE[@]}" -p "$PROJECT" up --build -d desk router

echo "==> wait for router /health"
ok=0
for i in $(seq 1 90); do
  if code=$(curl -sS -o /tmp/backlot-cp-health.json -w '%{http_code}' "http://127.0.0.1:${ROUTER_PORT}/health" 2>/dev/null); then
    if [[ "$code" == "200" ]]; then
      ok=1
      break
    fi
  fi
  sleep 1
done
if [[ "$ok" != "1" ]]; then
  echo "FAIL: router /health never became 200"
  "${COMPOSE[@]}" -p "$PROJECT" ps || true
  "${COMPOSE[@]}" -p "$PROJECT" logs || true
  exit 1
fi
echo "PASS: router /health 200 $(cat /tmp/backlot-cp-health.json)"

echo "==> desk /health"
desk_ok=0
for i in $(seq 1 30); do
  if dcode=$(curl -sS -o /tmp/backlot-desk-health.json -w '%{http_code}' "http://127.0.0.1:${DESK_PORT}/health" 2>/dev/null); then
    if [[ "$dcode" == "200" ]]; then
      desk_ok=1
      break
    fi
  fi
  sleep 1
done
if [[ "$desk_ok" != "1" ]]; then
  echo "FAIL: desk /health never became 200"
  "${COMPOSE[@]}" -p "$PROJECT" logs desk || true
  exit 1
fi
echo "PASS: desk /health 200 $(cat /tmp/backlot-desk-health.json)"

echo "==> lease without lot-boss must be 503"
lease_code=$(curl -sS -o /tmp/backlot-cp-lease.json -w '%{http_code}' \
  -X POST "http://127.0.0.1:${ROUTER_PORT}/v1/worlds" \
  -H 'content-type: application/json' \
  -d '{"profile":"demo"}')
echo "lease status=$lease_code body=$(cat /tmp/backlot-cp-lease.json)"
if [[ "$lease_code" != "503" ]]; then
  echo "FAIL: expected 503 world engine unavailable, got $lease_code"
  exit 1
fi
if ! grep -q 'world engine unavailable' /tmp/backlot-cp-lease.json; then
  echo "FAIL: 503 body missing 'world engine unavailable'"
  exit 1
fi
if grep -Eqi '"id"[[:space:]]*:[[:space:]]*"' /tmp/backlot-cp-lease.json; then
  echo "FAIL: lease 503 body must not invent a world id"
  exit 1
fi
echo "PASS: lease 503 world engine unavailable"

echo "==> desk+router must not have /dev/kvm"
for svc in desk router; do
  cid=$("${COMPOSE[@]}" -p "$PROJECT" ps -q "$svc")
  if [[ -z "$cid" ]]; then
    echo "FAIL: no container for $svc"
    exit 1
  fi
  devices=$(docker inspect -f '{{json .HostConfig.Devices}}' "$cid")
  privileged=$(docker inspect -f '{{.HostConfig.Privileged}}' "$cid")
  echo "$svc devices=$devices privileged=$privileged"
  if echo "$devices" | grep -q kvm; then
    echo "FAIL: $svc has kvm device mapping"
    exit 1
  fi
  if [[ "$privileged" == "true" ]]; then
    echo "FAIL: $svc is privileged"
    exit 1
  fi
  if docker exec "$cid" sh -c 'test -e /dev/kvm' 2>/dev/null; then
    echo "FAIL: $svc container has /dev/kvm node"
    exit 1
  fi
  echo "PASS: $svc has no /dev/kvm"
done

echo "ALL PASS: test-compose-cp"
