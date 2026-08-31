# Backlot — M1 inner ring + M2 one world
PYTHON ?= python3
export FIRECRACKER_BIN ?= /usr/local/firecracker/v1.15.1/firecracker
export JAILER_BIN ?= /usr/local/firecracker/v1.15.1/jailer

.PHONY: test test-unit test-int test-go test-m2 artifacts world-runtime rootfs kernel

test: test-unit test-int test-go

test-unit:
	cd inner && $(PYTHON) -m unittest discover -s tests/unit -t . -v

test-int:
	$(PYTHON) inner/tests/int/run_int.py

test-go:
	cd runtime && go test ./...

artifacts:
	$(PYTHON) inner/run.py --print-plan --dump-table inner/artifacts/syscall-table.txt > inner/artifacts/plan.txt

kernel:
	guest/fetch-kernel.sh

world-runtime:
	mkdir -p runtime/bin
	cd runtime && CGO_ENABLED=0 go build -o bin/world-runtime ./cmd/world-runtime
	cd runtime && CGO_ENABLED=0 go build -o bin/shepherd ./cmd/shepherd
	cd runtime && CGO_ENABLED=0 go build -o bin/m2test ./cmd/m2test

rootfs: world-runtime
	guest/build-rootfs.sh

# Skip cleanly without KVM (exit 2). Fail loud if KVM exists and the world leaks.
test-m2: kernel world-runtime
	@if [ ! -r /dev/kvm ]; then echo "NOT RUN: /dev/kvm is not readable"; exit 2; fi
	@if [ ! -f guest/artifacts/rootfs.ext4 ]; then $(MAKE) rootfs; fi
	runtime/bin/m2test
