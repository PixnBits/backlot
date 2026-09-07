# Compose control plane (M3 Phase 1)

Services:

| Service | Role | `/dev/kvm` |
|---|---|---|
| `desk` | Continuity desk (append-only events) | **No** |
| `router` | Session sticky map + HTTP facade | **No** |
| `lot-boss` (profile `kvm`) | Host shepherd for Firecracker — Phase 2 | Yes (host) |

## Start (no KVM required)

```bash
docker compose -f deploy/compose/docker-compose.yml up --build -d desk router
# host ports default 18080 (router) / 18090 (desk)
curl -sS localhost:18080/health
curl -sS -X POST localhost:18080/v1/worlds -H 'content-type: application/json' -d '{"profile":"demo"}'
# expect HTTP 503 {"error":"world engine unavailable"}
```

Override host ports if needed: `BACKLOT_ROUTER_HOST_PORT=8080 BACKLOT_DESK_HOST_PORT=8090`.

Or: `make test-compose-cp`

## Sit bars

1. `desk` and `router` containers must not have `/dev/kvm`.
2. Lease without a reachable host lot-boss/KVM engine → **503** (do not invent a world).
3. `/health` returns **200**.

Snapshot/restore endpoints return **501** (M4). Network phase `proxy` returns **501**.
