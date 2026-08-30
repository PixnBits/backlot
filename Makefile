# Backlot — M1 inner ring
PYTHON ?= python3

.PHONY: test test-unit test-int artifacts

test: test-unit test-int

# Pure Python 3. No bwrap, no gcc, no libseccomp.
test-unit:
	cd inner && $(PYTHON) -m unittest discover -s tests/unit -t . -v

# Real jail. Needs `bwrap` on PATH and gcc to build T1–T10.
test-int:
	$(PYTHON) inner/tests/int/run_int.py

artifacts:
	$(PYTHON) inner/run.py --print-plan --dump-table inner/artifacts/syscall-table.txt > inner/artifacts/plan.txt
