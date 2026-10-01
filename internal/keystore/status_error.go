package keystore

import (
	"fmt"
	"strings"
)

// Keep errors.Is/As usable, but never print an API key, including one echoed by
// the underlying storage driver. Reasons/actors are not included in this log.
type statusSyncError struct {
	vendorID string
	key      string
	cause    error
}

func (e *statusSyncError) Error() string {
	message := e.cause.Error()
	if e.key != "" {
		message = strings.ReplaceAll(message, e.key, "[redacted]")
	}
	return fmt.Sprintf("vendor=%s key_id=%s: %s", e.vendorID, KeyID(e.key), message)
}
func (e *statusSyncError) Unwrap() error { return e.cause }

func statusUpdateError(update pendingStatusUpdate, err error) error {
	return &statusSyncError{vendorID: update.vendorID, key: update.key, cause: err}
}
