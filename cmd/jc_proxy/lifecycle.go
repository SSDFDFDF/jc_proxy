package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// Track handlers, not connections: Server.Close cancels connections but does
// not wait for upstream attempts or administrative writes to finish settling.
type drainingHandler struct {
	next   http.Handler
	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
}

func (h *drainingHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	h.active.Add(1)
	h.mu.Unlock()
	defer h.active.Done()
	h.next.ServeHTTP(w, req)
}

func shutdownHTTPServer(ctx context.Context, server *http.Server, handler *drainingHandler) error {
	err := server.Shutdown(ctx)
	handler.mu.Lock()
	handler.closed = true // no Add may race the following Wait
	handler.mu.Unlock()
	if err != nil {
		// Unblock client reads/writes and propagate request cancellation into
		// upstream transports before the last stats/status flush can occur.
		err = errors.Join(err, server.Close())
	}
	handler.active.Wait()
	return err
}
