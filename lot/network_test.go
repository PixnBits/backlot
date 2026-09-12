package lot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNetworkUnknownWorld(t *testing.T) {
	boss := NewBoss(3, 0, true)
	req := httptest.NewRequest(http.MethodPut, "/v1/worlds/nope/network", strings.NewReader(`{"phase":"proxy"}`))
	rr := httptest.NewRecorder()
	boss.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNetworkBadPhase(t *testing.T) {
	boss := NewBoss(3, 0, true)
	_, err := boss.SetNetwork("nope", "open")
	if err == nil || !strings.Contains(err.Error(), "dark or proxy") {
		t.Fatalf("err=%v", err)
	}
}
