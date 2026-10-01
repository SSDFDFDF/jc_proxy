package gateway

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// Reuse the already allocated attempt as the response body observer. No body
// buffering, tracing callbacks, extra timers or per-attempt allocations.
func (a *upstreamAttempt) Read(p []byte) (int, error) {
	n, err := a.body.Read(p)
	if err != nil && a.bodyErr == nil {
		a.bodyErr = err
		a.fullElapsed = time.Since(a.started)
	}
	return n, err
}

func (a *upstreamAttempt) Close() error {
	if a.closed {
		return nil
	}
	a.closed = true
	err := a.body.Close()
	if a.request.Context().Err() != nil {
		return err // client cancellation is not evidence of an unhealthy key
	}
	complete := errors.Is(a.bodyErr, io.EOF)
	// A successful response closed before EOF without a read error is normally
	// a downstream write failure. Do not score the upstream for that failure.
	if a.bodyErr == nil && a.responseStatus < http.StatusBadRequest {
		return err
	}
	full := time.Duration(-1)
	if complete {
		full = a.fullElapsed
	}
	// Discarded retry/masked error bodies still count as failed attempts, but
	// only bodies read to EOF contribute to the complete-response average.
	a.vendor.pool.RecordSample(a.idx, a.headerElapsed, full, complete && a.responseStatus < http.StatusBadRequest, a.selectedVersion)
	return err
}
