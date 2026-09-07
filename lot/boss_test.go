package lot

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPhase1LeaseRefusesWithoutEngine(t *testing.T) {
	boss := NewBoss(3, 0, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/worlds", nil)
	rr := httptest.NewRecorder()
	boss.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
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
}
