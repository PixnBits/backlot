package lot

import (
	"log"
	"time"
)

func parseTTLPause(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 15 * time.Minute
	}
	return d
}

func (s *slot) considerPause(now time.Time) {
	if s == nil || s.paused || s.ttlPause <= 0 {
		return
	}
	if now.Sub(s.lastActive) < s.ttlPause {
		return
	}
	if s.w != nil {
		if err := s.w.Pause(); err != nil {
			log.Printf("lot-boss: pause %s: %v", s.info.ID, err)
			return
		}
	}
	s.paused = true
	s.info.State = "paused"
}

func (s *slot) onActivity() {
	if s == nil {
		return
	}
	if s.paused && s.w != nil {
		if err := s.w.Resume(); err != nil {
			log.Printf("lot-boss: resume %s: %v", s.info.ID, err)
			return
		}
	}
	s.paused = false
	s.info.State = "running"
	s.lastActive = time.Now()
}

func (b *Boss) pauseLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for range tick.C {
		b.mu.Lock()
		if b.stopping {
			b.mu.Unlock()
			return
		}
		now := time.Now()
		for _, s := range b.worlds {
			s.considerPause(now)
		}
		b.mu.Unlock()
	}
}
