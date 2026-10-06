package admin

import (
	"encoding/json"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func TestPerformanceSettingsVendorUpdates(t *testing.T) {
	s := makeHandlerForTest(t).service
	defer s.runtime.Close()
	cfg, err := s.GetConfigRaw()
	if err != nil {
		t.Fatal(err)
	}
	v := cfg.Vendors[0]
	ttl := 90 * time.Second
	power := 3.0
	v.LoadBalance = "adaptive"
	v.Performance = &config.PerformanceConfig{SampleTTL: &ttl, SuccessPower: &power}
	if err := s.UpdateVendor("admin", v.ID, v.VendorConfig); err != nil {
		t.Fatal(err)
	}
	assertSettings := func(wantTTL time.Duration, wantPower float64) {
		t.Helper()
		cfg, err := s.GetConfigRaw()
		if err != nil {
			t.Fatal(err)
		}
		s := cfg.Vendors[0].Performance.Resolve(cfg.Vendors[0].LoadBalance)
		if s.SampleTTL != wantTTL || s.SuccessPower != wantPower {
			t.Fatalf("settings=%+v", s)
		}
	}
	assertSettings(ttl, power)
	v.Performance = nil // legacy client did not submit section
	if err := s.UpdateVendor("admin", v.ID, v.VendorConfig); err != nil {
		t.Fatal(err)
	}
	assertSettings(ttl, power)
	// Explicit {} resets; it must survive JSON/YAML Clone (not collapse to nil).
	if err := json.Unmarshal([]byte(`{"performance":{}}`), &v.VendorConfig); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateVendor("admin", v.ID, v.VendorConfig); err != nil {
		t.Fatal(err)
	}
	assertSettings(5*time.Minute, 2)
	v.LoadBalance = "lowest_latency"
	if err := s.UpdateVendor("admin", v.ID, v.VendorConfig); err != nil {
		t.Fatal(err)
	}
	assertSettings(5*time.Minute, 1)
	invalid := 0 * time.Second
	v.Performance = &config.PerformanceConfig{SampleTTL: &invalid}
	if err := s.UpdateVendor("admin", v.ID, v.VendorConfig); err == nil {
		t.Fatal("accepted invalid TTL")
	}
	assertSettings(5*time.Minute, 1)
}
