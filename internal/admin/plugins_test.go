package admin

import (
	"testing"

	"jc_proxy/internal/config"
)

func TestAdminVendorPluginPreservation(t *testing.T) {
	prev := config.VendorConfig{Plugins: []config.PluginConfig{{Name: "history", Builtin: "codex-history-filter"}}}
	if got := mergeVendorConfigForAdminUpsert(prev, config.VendorConfig{}); len(got.Plugins) != 1 {
		t.Fatal("old admin client cleared plugins")
	}
	if got := mergeVendorConfigForAdminUpsert(prev, config.VendorConfig{Plugins: []config.PluginConfig{}}); len(got.Plugins) != 0 {
		t.Fatal("explicit clear ignored")
	}
	if got := mergeVendorConfigForAdminUpsert(prev, config.VendorConfig{Provider: "aggregate"}); len(got.Plugins) != 0 {
		t.Fatal("carried plugins into aggregate")
	}
}
