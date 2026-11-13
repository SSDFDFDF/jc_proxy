package config

import (
	"strings"
	"testing"
	"time"
)

func TestPluginConfigurationValidation(t *testing.T) {
	valid := PluginConfig{Name: "history", Builtin: "codex-history-filter", Methods: []string{"POST"}, Paths: []string{"/v1/responses", "/responses/*"}}
	for _, tc := range []struct {
		name string
		edit func(*PluginConfig)
	}{
		{"no implementation", func(p *PluginConfig) { p.Builtin = "" }},
		{"two implementations", func(p *PluginConfig) { p.Source = "function onRequest() {}" }},
		{"empty name", func(p *PluginConfig) { p.Name = "" }},
		{"negative timeout", func(p *PluginConfig) { p.Timeout = -time.Second }},
		{"excessive timeout", func(p *PluginConfig) { p.Timeout = 6 * time.Second }},
		{"negative limit", func(p *PluginConfig) { p.MaxBodyBytes = -1 }},
		{"excessive limit", func(p *PluginConfig) { p.MaxBodyBytes = 65 << 20 }},
		{"lowercase method", func(p *PluginConfig) { p.Methods = []string{"post"} }},
		{"relative path", func(p *PluginConfig) { p.Paths = []string{"v1/responses"} }},
		{"middle wildcard", func(p *PluginConfig) { p.Paths = []string{"/v1/*/responses"} }},
		{"query", func(p *PluginConfig) { p.Paths = []string{"/v1/responses?x=1"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.edit(&p)
			if err := validatePlugins([]PluginConfig{p}, false); err == nil {
				t.Fatal("invalid plugin accepted")
			}
		})
	}
	if err := validatePlugins([]PluginConfig{valid}, false); err != nil {
		t.Fatal(err)
	}
	if err := validatePlugins([]PluginConfig{valid, valid}, false); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := validatePlugins([]PluginConfig{valid}, true); err == nil || !strings.Contains(err.Error(), "non-aggregate") {
		t.Fatalf("aggregate plugin: %v", err)
	}
	if valid.ExecutionTimeout() != DefaultPluginTimeout || valid.BodyLimit() != DefaultPluginMaxBodyBytes {
		t.Fatal("invalid defaults")
	}
}

func TestPluginsSurviveConfigClone(t *testing.T) {
	cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"test": {
		Upstream: UpstreamConfig{BaseURL: "https://example.invalid"},
		Plugins:  []PluginConfig{{Name: "script", Source: "function onRequest(r) { return r.body; }", Methods: []string{"POST"}, Timeout: time.Second}},
	}})}
	cloned, err := cfg.Clone()
	if err != nil {
		t.Fatal(err)
	}
	p := cloned.Vendors[0].Plugins[0]
	if p.Source != cfg.Vendors[0].Plugins[0].Source || p.Timeout != time.Second {
		t.Fatalf("plugin lost: %+v", p)
	}
	cloned.Vendors[0].Plugins[0].Methods[0] = "GET"
	if cfg.Vendors[0].Plugins[0].Methods[0] != "POST" {
		t.Fatal("plugin configuration aliased")
	}
}
