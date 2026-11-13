package config

import (
	"fmt"
	"strings"
	"time"
)

const (
	DefaultPluginTimeout            = 100 * time.Millisecond
	DefaultPluginMaxBodyBytes int64 = 4 << 20
	MaxPluginScriptBytes            = 1 << 20
)

// PluginConfig describes one ordered request-body transform. Scripts are read
// when building a router, never from the request path.
type PluginConfig struct {
	Name         string        `yaml:"name" json:"name"`
	Enabled      *bool         `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Builtin      string        `yaml:"builtin,omitempty" json:"builtin,omitempty"`
	Script       string        `yaml:"script,omitempty" json:"script,omitempty"`
	Source       string        `yaml:"source,omitempty" json:"source,omitempty"`
	Methods      []string      `yaml:"methods,omitempty" json:"methods,omitempty"`
	Paths        []string      `yaml:"paths,omitempty" json:"paths,omitempty"`
	Timeout      time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	MaxBodyBytes int64         `yaml:"max_body_bytes,omitempty" json:"max_body_bytes,omitempty"`
}

func (p PluginConfig) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

func (p PluginConfig) ExecutionTimeout() time.Duration {
	if p.Timeout == 0 {
		return DefaultPluginTimeout
	}
	return p.Timeout
}

func (p PluginConfig) BodyLimit() int64 {
	if p.MaxBodyBytes == 0 {
		return DefaultPluginMaxBodyBytes
	}
	return p.MaxBodyBytes
}

func validatePlugins(plugins []PluginConfig, aggregate bool) error {
	if aggregate && len(plugins) > 0 {
		return fmt.Errorf("plugins are only supported on non-aggregate vendors; configure plugins on child vendors")
	}
	if len(plugins) > 16 {
		return fmt.Errorf("plugins must contain at most 16 entries")
	}
	names := make(map[string]bool, len(plugins))
	for i, p := range plugins {
		if p.Name == "" || strings.TrimSpace(p.Name) != p.Name || len(p.Name) > 64 || strings.ContainsAny(p.Name, "\r\n\t") {
			return fmt.Errorf("plugins[%d].name must be a non-empty name of at most 64 bytes without surrounding whitespace", i)
		}
		if names[p.Name] {
			return fmt.Errorf("duplicate plugin name %q", p.Name)
		}
		names[p.Name] = true
		implementations := 0
		for _, value := range []string{p.Builtin, p.Script, p.Source} {
			if strings.TrimSpace(value) != "" {
				implementations++
			}
		}
		if implementations != 1 {
			return fmt.Errorf("plugin %q requires exactly one of builtin, script or source", p.Name)
		}
		if len(p.Source) > MaxPluginScriptBytes {
			return fmt.Errorf("plugin %q source exceeds 1 MiB", p.Name)
		}
		if p.Timeout < 0 || p.Timeout > 5*time.Second {
			return fmt.Errorf("plugin %q timeout must be between 0 and 5s", p.Name)
		}
		if p.MaxBodyBytes < 0 || p.MaxBodyBytes > 64<<20 {
			return fmt.Errorf("plugin %q max_body_bytes must be between 0 and 67108864", p.Name)
		}
		for _, method := range p.Methods {
			if method == "" || strings.IndexFunc(method, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
				return fmt.Errorf("plugin %q methods must use uppercase HTTP methods", p.Name)
			}
		}
		for _, path := range p.Paths {
			if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\r\n\t ") || strings.Contains(strings.TrimSuffix(path, "*"), "*") {
				return fmt.Errorf("plugin %q paths must start with / and may only use a trailing *", p.Name)
			}
		}
	}
	return nil
}
