package gateway

import (
	"net/http"
	"sync"
	"time"
)

type interimResponseSender struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	interval time.Duration

	mu        sync.Mutex
	committed bool
	stopped   bool
	timer     *time.Timer
}

func newInterimResponseSender(w http.ResponseWriter, interval time.Duration) *interimResponseSender {
	s := &interimResponseSender{
		w:        w,
		interval: interval,
	}
	if flusher, ok := w.(http.Flusher); ok {
		s.flusher = flusher
	}
	if interval <= 0 {
		return s
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
	s.w.WriteHeader(http.StatusProcessing)
	if s.flusher != nil {
		s.flusher.Flush()
	}
	if s.timer != nil {
		s.timer.Reset(s.interval)
	}
}

func (s *interimResponseSender) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (s *interimResponseSender) commitFinal(fn func()) {
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
