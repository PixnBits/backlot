# Backlot — M1 inner ring + M2 one world + M3 compose CP + Phase 2 fleet + Phase 3 pause/proxy
PYTHON ?= python3
export FIRECRACKER_BIN ?= /usr/local/firecracker/v1.15.1/firecracker
export JAILER_BIN ?= /usr/local/firecracker/v1.15.1/jailer

.PHONY: test test-unit test-int test-go test-m2 test-m3 test-compose-cp artifacts world-runtime rootfs kernel lot-bins

test: test-unit test-int test-go

test-unit:
	cd inner && $(PYTHON) -m unittest discover -s tests/unit -t . -v

test-int:
	$(PYTHON) inner/tests/int/run_int.py

test-go:
	cd runtime && go test ./...
	cd desk && go test ./...
	cd router && go test ./...
	cd lot && go test ./...
	cd proxy && go test ./...

artifacts:
	$(PYTHON) inner/run.py --print-plan --dump-table inner/artifacts/syscall-table.txt > inner/artifacts/plan.txt

kernel:
	guest/fetch-kernel.sh

world-runtime:
	mkdir -p runtime/bin
	cd runtime && CGO_ENABLED=0 go build -o bin/world-runtime ./cmd/world-runtime
	cd runtime && CGO_ENABLED=0 go build -o bin/shepherd ./cmd/shepherd
	cd runtime && CGO_ENABLED=0 go build -o bin/m2test ./cmd/m2test
	cd runtime && CGO_ENABLED=0 go build -o bin/fc-waitkvm ./cmd/fc-waitkvm

lot-bins:
	mkdir -p lot/bin
	cd lot && CGO_ENABLED=0 go build -o bin/lot-boss ./cmd/lot-boss
	cd lot && CGO_ENABLED=0 go build -o bin/m3test ./cmd/m3test

rootfs: world-runtime
	guest/build-rootfs.sh

# Skip cleanly without KVM (exit 2). Fail loud if KVM exists and the world leaks.
# Rebuild rootfs if init.sh or world-runtime is newer (guest image bakes both).
test-m2: kernel world-runtime
	@if [ ! -r /dev/kvm ]; then echo "NOT RUN: /dev/kvm is not readable"; exit 2; fi
	@if [ ! -f guest/artifacts/rootfs.ext4 ] || [ guest/init.sh -nt guest/artifacts/rootfs.ext4 ] || [ runtime/bin/world-runtime -nt guest/artifacts/rootfs.ext4 ]; then $(MAKE) rootfs; fi
	runtime/bin/m2test

# Phase 1: compose desk+router (no KVM). /health 200; lease → 503; no /dev/kvm in CP containers.
test-compose-cp:
	./scripts/test-compose-cp.sh

# Phase 2: three jailed worlds via lot-boss. Exit 2 without KVM or without jailer/sudo.
# Prefer: sudo -E ./scripts/m3test-root.sh
test-m3: lot-bins
	@if [ ! -r /dev/kvm ]; then echo "NOT RUN: /dev/kvm is not readable"; exit 2; fi
	@if [ "$$(id -u)" -ne 0 ]; then echo "NOT RUN: need root/jailer — run: sudo -E ./scripts/m3test-root.sh"; exit 2; fi
	lot/bin/m3test
