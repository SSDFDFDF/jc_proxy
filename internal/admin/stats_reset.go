package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var errInvalidStatsReset = errors.New("invalid statistics reset")

type RuntimeStatsResetRequest struct {
	Scope        string `json:"scope"` // "vendor" or "all"; never inferred from an empty id
	VendorID     string `json:"vendor_id,omitempty"`
	Confirmation string `json:"confirmation"`
}

func (s *Service) ResetRuntimeStats(actor string, req RuntimeStatsResetRequest) (int, error) {
	req.VendorID = strings.TrimSpace(req.VendorID)
	if req.Confirmation != "RESET" {
		return 0, fmt.Errorf("%w: confirmation must be RESET", errInvalidStatsReset)
	}
	switch req.Scope {
	case "all":
		if req.VendorID != "" {
			return 0, fmt.Errorf("%w: all scope must not include vendor_id", errInvalidStatsReset)
		}
	case "vendor":
		if req.VendorID == "" {
			return 0, fmt.Errorf("%w: vendor_id is required", errInvalidStatsReset)
		}
	default:
		return 0, fmt.Errorf("%w: scope must be vendor or all", errInvalidStatsReset)
	}

	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	if req.Scope == "vendor" {
		cfg, err := s.store.GetConfig()
		if err != nil {
			return 0, err
		}
		entry, configured := cfg.VendorByID(req.VendorID)
		if configured && entry.Provider == "aggregate" {
			return 0, fmt.Errorf("%w: reset the aggregate's child vendors or use all scope", errInvalidStatsReset)
		}
		if !configured {
			// Orphan key partitions are visible in the key hub and are valid
			// reset targets even when they have no current router.
			records, err := s.keyStore.List(req.VendorID)
			if err != nil {
				return 0, err
			}
			if len(records) == 0 {
				return 0, fmt.Errorf("%w: unknown vendor_id", errInvalidStatsReset)
			}
		}
	}
	if s.statsPersister == nil {
		return 0, errors.New("runtime stats reset is unavailable")
	}
	count, err := s.statsPersister.Reset(req.VendorID)
	detail := map[string]any{"scope": req.Scope, "vendor_id": req.VendorID}
	if err != nil {
		s.audit.Log(actor, "runtime_stats.reset_failed", detail)
		return 0, err
	}
	detail["count"] = count
	s.audit.Log(actor, "runtime_stats.reset", detail)
	return count, nil
}

func (h *Handler) handleStatsReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req RuntimeStatsResetRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid reset json body")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "expected one reset request")
		return
	}
	count, err := h.service.ResetRuntimeStats(r.Header.Get("X-Admin-User"), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errInvalidStatsReset) {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": count})
}
