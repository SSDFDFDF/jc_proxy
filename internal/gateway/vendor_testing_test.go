package gateway

import (
	"context"
	"strings"
	"testing"

	"jc_proxy/internal/config"
)

// Aggregate vendors are built without an upstream base URL. The vendor-test
// paths used to dereference that nil *url.URL and panic inside net/url, which
// surfaced to clients as a vanished connection instead of an error response.
func TestVendorTestMetaAggregateVendorReturnsErrorInsteadOfPanicking(t *testing.T) {
	cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{})
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := newTestRouter(cfg, aggregateTestSeed())
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	aggregateID := vendorID(t, cfg, "agg")
	if _, err := router.VendorTestMeta(aggregateID); err == nil {
		t.Fatal("expected an error for the aggregate vendor")
	} else if !strings.Contains(err.Error(), "no upstream base_url") {
		t.Fatalf("unexpected error message: %v", err)
	}

	if _, err := router.VendorTestMeta(vendorID(t, cfg, "child_a")); err != nil {
		t.Fatalf("child vendor meta should still work: %v", err)
	}
}

// A probe for an aggregate vendor must fail fast with the same descriptive
// error, while an explicit base_url keeps the generic path usable.
func TestExecuteVendorTestAggregateVendorErrorsWithoutExplicitBaseURL(t *testing.T) {
	cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{})
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := newTestRouter(cfg, aggregateTestSeed())
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	aggregateID := vendorID(t, cfg, "agg")
	_, err = router.ExecuteVendorTest(context.Background(), aggregateID, VendorTestRequest{Endpoint: "/v1/models"})
	if err == nil {
		t.Fatal("expected an error for the aggregate vendor")
	} else if !strings.Contains(err.Error(), "no upstream base_url") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
