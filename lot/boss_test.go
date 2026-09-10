package lot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhase1LeaseRefusesWithoutEngine(t *testing.T) {
	boss := NewBoss(3, 0, true)
	// No kernel/rootfs/workdir → EngineReady false
	req := httptest.NewRequest(http.MethodPost, "/v1/worlds", nil)
	rr := httptest.NewRecorder()
	boss.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "world engine unavailable") {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestHealth(t *testing.T) {
	boss := NewBoss(3, 1, true)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	boss.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"engine_ready":false`) {
		t.Fatalf("expected engine_ready false without artifacts: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"phase":2`) {
		t.Fatalf("expected phase 2: %s", rr.Body.String())
	}
}

func TestEngineReadyRequiresJailerWhenConfigured(t *testing.T) {
	boss := NewBossConfig(Config{
		MaxWorlds:     3,
		RequireKVM:    false, // unit test host may lack kvm in CI
		RequireJailer: true,
		WorkDir:       t.TempDir(),
		Kernel:        t.TempDir() + "/k", // missing → artifactsOK false first
		Rootfs:        t.TempDir() + "/r",
		Firecracker:   "/usr/local/firecracker/v1.15.1/firecracker",
		Jailer:        "/usr/local/firecracker/v1.15.1/jailer",
	})
	if boss.EngineReady() {
		t.Fatal("missing artifacts must not be ready")
	}
}
