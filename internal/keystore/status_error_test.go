package keystore

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestStatusSyncErrorsRedactKeysAndPreserveCause(t *testing.T) {
	key := "sk-private-key-must-not-be-logged"
	cause := fmt.Errorf("driver echoed %s: %w", key, ErrVersionMismatch)
	err := statusUpdateError(pendingStatusUpdate{vendorID: "vid_test", key: key}, cause)
	if strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "key_id="+KeyID(key)) {
		t.Fatalf("unsafe diagnostic: %s", err)
	}
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatal("error identity lost")
	}
}
