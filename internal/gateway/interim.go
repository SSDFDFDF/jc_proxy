package gateway

import (
	"net/http"
	"sync"
	"time"
)

type interimResponseSender struct {
	w        http.ResponseWriter
	interval time.Duration

	mu        sync.Mutex
	committed bool
	stopped   bool
	timer     *time.Timer
}

func newInterimResponseSender(w http.ResponseWriter, interval time.Duration) *interimResponseSender {
	if interval <= 0 {
		return nil
	}
	s := &interimResponseSender{
		w:        w,
		interval: interval,
	}
	s.mu.Lock()
	s.timer = time.AfterFunc(interval, s.tick)
	s.mu.Unlock()
	return s
}

func (s *interimResponseSender) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed || s.stopped {
		return
	}
	// net/http sends informational headers immediately. Flush here would
	// implicitly commit a final 200 before the actual upstream status exists.
	s.w.WriteHeader(http.StatusProcessing)
	if s.timer != nil {
		s.timer.Reset(s.interval)
	}
}

func (s *interimResponseSender) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (s *interimResponseSender) commitFinal(fn func()) {
	if s == nil {
		fn()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.committed {
		return
	}
	fn()
	s.committed = true
}
