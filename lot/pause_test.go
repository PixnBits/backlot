package lot

import (
	"testing"
	"time"
)

func TestParseTTLPause(t *testing.T) {
	if parseTTLPause("") != 15*time.Minute {
		t.Fatal("empty")
	}
	if parseTTLPause("bogus") != 15*time.Minute {
		t.Fatal("bogus")
	}
	if parseTTLPause("50ms") != 50*time.Millisecond {
		t.Fatal("50ms")
	}
}

func TestConsiderPauseHoldsRAMState(t *testing.T) {
	s := &slot{
		info:       World{ID: "w1", State: "running"},
		lastActive: time.Now().Add(-200 * time.Millisecond),
		ttlPause:   50 * time.Millisecond,
	}
	s.considerPause(time.Now())
	if !s.paused || s.info.State != "paused" {
		t.Fatalf("want paused, got paused=%v state=%s", s.paused, s.info.State)
	}
	s.onActivity()
	if s.paused || s.info.State != "running" {
		t.Fatalf("want running after activity, got paused=%v state=%s", s.paused, s.info.State)
	}
}

func TestConsiderPauseNotYet(t *testing.T) {
	s := &slot{
		info:       World{ID: "w1", State: "running"},
		lastActive: time.Now(),
		ttlPause:   time.Hour,
	}
	s.considerPause(time.Now())
	if s.paused {
		t.Fatal("must not pause before ttl")
	}
}
