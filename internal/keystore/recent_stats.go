package keystore

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// RecentStats is a replacement snapshot, NOT an additive counter delta.
// Only aggregates are persisted; the five raw samples stay in memory.
type RecentStats struct {
	RecentRequests     int     `json:"recent_requests"`
	RecentSuccessCount int     `json:"recent_success_count"`
	HeaderSamples      int     `json:"header_samples"`
	AvgHeaderMS        float64 `json:"avg_header_ms"`
	ResponseSamples    int     `json:"response_samples"`
	AvgResponseMS      float64 `json:"avg_response_ms"`
}

func (s RecentStats) Value() (driver.Value, error) {
	data, err := json.Marshal(s)
	return string(data), err
}

func (s *RecentStats) Scan(value any) error {
	*s = RecentStats{}
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return json.Unmarshal(v, s)
	case string:
		return json.Unmarshal([]byte(v), s)
	default:
		return fmt.Errorf("invalid recent stats value type %T", value)
	}
}
