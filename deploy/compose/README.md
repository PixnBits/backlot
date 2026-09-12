# Compose control plane (M3 Phase 1 + Phase 2 demo)

Services:

| Service | Role | `/dev/kvm` |
|---|---|---|
| `desk` | Continuity desk (append-only events) | **No** |
| `router` | Session sticky map + HTTP facade | **No** |
| `lot-boss` (profile `kvm`) | Host shepherd for Firecracker — Phase 2 | Yes (host) |

Packing rule: lot-boss may be privileged **host shepherd**. It is **not** three microVMs inside one container. One world = one jailer+Firecracker = one guest kernel. Cap 3. Warm pool 0 or 1.

## Start control plane (no KVM required)

```bash
docker compose -f deploy/compose/docker-compose.yml up --build -d desk router
# host ports default 18080 (router) / 18090 (desk)
curl -sS localhost:18080/health
curl -sS -X POST localhost:18080/v1/worlds -H 'content-type: application/json' -d '{"profile":"demo"}'
# expect HTTP 503 {"error":"world engine unavailable"}
```

Override host ports if needed: `BACKLOT_ROUTER_HOST_PORT=8080 BACKLOT_DESK_HOST_PORT=8090`.

Or: `make test-compose-cp`

## Sit bars (Phase 1)

1. `desk` and `router` containers must not have `/dev/kvm`.
2. Lease without a reachable host lot-boss/KVM engine → **503** (do not invent a world).
3. `/health` returns **200**.

Snapshot/restore endpoints return **501** (M4). Network phase `dark` / `proxy` is live. `proxy` is the userspace egress-proxy container (default deny). Guests still have no NIC.

## Phase 2 demo — three real worlds on the KVM host

Requires readable `/dev/kvm`, Firecracker/jailer **v1.15.1**, guest artifacts, and **root** for jailer (`SUDO_UID` set).

### A. Preferred: host binaries (shepherd on host, CP in Compose)

```bash
# 1) Control plane (desk+router). Point router at host lot-boss.
export BACKLOT_ROUTER_HOST_PORT=18080 BACKLOT_DESK_HOST_PORT=18090
docker compose -f deploy/compose/docker-compose.yml up --build -d desk router
# recreate router with LOT_BOSS_URL → host gateway
docker compose -f deploy/compose/docker-compose.yml up -d --no-deps \
  -e LOT_BOSS_URL=http://172.17.0.1:8070 router
# or edit compose to set LOT_BOSS_URL + extra_hosts host.docker.internal:host-gateway

# 2) Desk must be reachable from lot-boss on the host
#    If desk is published on 18090:
sudo -E ./lot/bin/lot-boss \
  --addr :8070 \
  --max-worlds 3 \
  --warm-pool 0 \
  --desk http://127.0.0.1:18090 \
  --work /tmp/backlot-lot \
  --kernel "$PWD/guest/artifacts/vmlinux-6.1.102" \
  --rootfs "$PWD/guest/artifacts/rootfs.ext4"
```

Build binaries first: `make world-runtime lot-bins` (and `make rootfs` / copy artifacts if needed).

### B. Demo curls (router on :18080)

```bash
# Lease three worlds
curl -sS -X POST localhost:18080/v1/worlds -H 'content-type: application/json' -d '{"profile":"demo"}'
curl -sS -X POST localhost:18080/v1/worlds -H 'content-type: application/json' -d '{"profile":"demo"}'
curl -sS -X POST localhost:18080/v1/worlds -H 'content-type: application/json' -d '{"profile":"demo"}'
# Fourth should 503 (cap 3)

WID=... # paste id from first lease

# Exec through inner/run.py
curl -sS -X POST "localhost:18080/v1/worlds/$WID/exec" \
  -H 'content-type: application/json' \
  -d '{"argv":["/usr/bin/ls","/workspace"],"timeout":30}'

# Decoy open → desk
curl -sS -X POST "localhost:18080/v1/worlds/$WID/exec" \
  -H 'content-type: application/json' \
  -d '{"argv":["/usr/bin/cat","/opt/grok/CANARY.txt"],"timeout":30}'
curl -sS "localhost:18080/v1/worlds/$WID/events"

# Scale down one world
curl -sS -X DELETE "localhost:18080/v1/worlds/$WID"

# Demand: lease a replacement (live ≤ 3)
curl -sS -X POST localhost:18080/v1/worlds -H 'content-type: application/json' -d '{"profile":"demo"}'
```

### C. Fleet test

```bash
# exit 2 without KVM or without root/jailer
make test-m3
# jailer sitting:
sudo -E ./scripts/m3test-root.sh
```

See `lot/TEST_REPORT.md`. Phase 3: `ttl_pause` (Firecracker Pause, RAM held) + userspace `proxy`. `ttl_store` / `ttl_prune` still deferred.
