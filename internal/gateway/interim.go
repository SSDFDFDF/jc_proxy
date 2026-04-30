package gateway

import (
	"net/http"
	"sync"
	"time"
)

type interimResponseSender struct {
	mu        sync.Mutex
	w         http.ResponseWriter
	flusher   http.Flusher
	committed bool
	stopCh    chan struct{}
	doneCh    chan struct{}
	stopOnce  sync.Once
}

func newInterimResponseSender(w http.ResponseWriter, interval time.Duration) *interimResponseSender {
	s := &interimResponseSender{
		w:      w,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	if flusher, ok := w.(http.Flusher); ok {
		s.flusher = flusher
	}
	if interval <= 0 {
		close(s.doneCh)
		return s
	}
	go s.run(interval)
	return s
}

func (s *interimResponseSender) run(interval time.Duration) {
	defer close(s.doneCh)

	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			s.writeInterim(http.StatusProcessing)
			timer.Reset(interval)
		case <-s.stopCh:
			return
		}
	}
}

func (s *interimResponseSender) writeInterim(statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed {
		return
	}
	s.w.WriteHeader(statusCode)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *interimResponseSender) stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	<-s.doneCh
}

func (s *interimResponseSender) commitFinal(fn func()) {
	s.stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed {
		return
	}
	fn()
	s.committed = true
}
