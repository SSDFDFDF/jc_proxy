package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"jc_proxy/internal/config"
)

type configBackend interface {
	Load() (*loadedConfig, error)
	Save(*config.Config) error
	Close() error
}

// newRemoteConfigBackend is a seam so tests can fake the PostgreSQL backend
// without a database.
var newRemoteConfigBackend = func(cfg config.ConfigStorePGSQLConfig) (configBackend, error) {
	return newPGConfigBackend(cfg)
}

type loadedConfig struct {
	cfg        *config.Config
	adminLayer config.AdminCredentialLayer
}

type Store struct {
	path              string
	remote            configBackend
	useRemote         bool
	bootstrap         config.StorageConfig
	initAdminPassword string
	mu                sync.RWMutex
	cfg               *config.Config
}

type RuntimeStats struct {
	Vendor string                 `json:"vendor"`
	Keys   []map[string]any       `json:"keys"`
	Extra  map[string]interface{} `json:"extra,omitempty"`
}

func NewStore(configPath string, bootstrap *config.Config) (*Store, error) {
	if bootstrap == nil {
		return nil, errors.New("bootstrap config is nil")
	}

	s := &Store{
		path:      strings.TrimSpace(configPath),
		bootstrap: bootstrap.Storage,
	}

	effective, err := bootstrap.Clone()
	if err != nil {
		return nil, err
	}
	seedRemote := false
	// Ids minted during load exist only in memory; unless they are persisted
	// below, the next restart mints different ones and every key partition and
	// runtime statistic stored under the old ids is orphaned.
	mintedIDs := bootstrap.MintedVendorIDs()

	if bootstrap.Storage.Config.Driver == "pgsql" {
		backend, err := newRemoteConfigBackend(bootstrap.Storage.Config.PGSQL)
		if err != nil {
			return nil, err
		}
		s.remote = backend
		s.useRemote = true

		loaded, err := backend.Load()
		if err != nil {
			_ = backend.Close()
			return nil, err
		}
		if loaded != nil {
			nextEffective, err := mergeLoadedConfig(effective, loaded)
			if err != nil {
				_ = backend.Close()
				return nil, err
			}
			effective = nextEffective
			// The remote payload replaced the bootstrap vendors, so from here
			// on only its own minting matters.
			mintedIDs = loaded.cfg.MintedVendorIDs()
		} else {
			seedRemote = true
		}
	}

	if err := s.ensureBootstrapAdminPassword(effective); err != nil {
		if s.remote != nil {
			_ = s.remote.Close()
		}
		return nil, err
	}

	s.cfg, err = sanitizeConfigForStore(effective, bootstrap.Storage)
	if err != nil {
		if s.remote != nil {
			_ = s.remote.Close()
		}
		return nil, err
	}
	if s.useRemote && (seedRemote || mintedIDs || s.initAdminPassword != "") {
		if err := s.remote.Save(s.cfg); err != nil {
			_ = s.remote.Close()
			return nil, err
		}
	}
	// In remote mode the local file is only a bootstrap layer whose vendors
	// are ignored on the next boot, so minted ids alone are no reason to
	// rewrite a hand-maintained file.
	writeLocal := s.initAdminPassword != "" || (!s.useRemote && mintedIDs)
	if writeLocal && s.path != "" {
		if err := writeConfigFile(s.path, s.cfg); err != nil {
			if s.remote != nil {
				_ = s.remote.Close()
			}
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.remote == nil {
		return nil
	}
	return s.remote.Close()
}

func (s *Store) GetConfig() (*config.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return nil, errors.New("empty config")
	}
	return s.cfg.Clone()
}

func (s *Store) GeneratedAdminPassword() string {
	if s == nil {
		return ""
	}
	return s.initAdminPassword
}

func (s *Store) UpdateConfig(next *config.Config) error {
	if next == nil {
		return errors.New("config is nil")
	}
	sanitized, err := sanitizeConfigForStore(next, s.bootstrap)
	if err != nil {
		return err
	}

	if s.useRemote {
		if err := s.remote.Save(sanitized); err != nil {
			return err
		}
	}
	if s.path != "" {
		if err := writeConfigFile(s.path, sanitized); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.cfg = sanitized
	s.mu.Unlock()
	return nil
}

func (s *Store) SnapshotJSON(maskSecrets bool) ([]byte, error) {
	cfg, err := s.GetConfig()
	if err != nil {
		return nil, err
	}
	if maskSecrets {
		for i := range cfg.Vendors {
			if !cfg.Vendors[i].ClientAuth.Enabled {
				continue
			}
			keys := cfg.Vendors[i].ClientAuth.Keys
			for j := range keys {
				keys[j] = mask(keys[j])
			}
		}
		cfg.Admin.Password = "******"
	}
	return json.Marshal(cfg)
}

func sanitizeConfigForStore(next *config.Config, storage config.StorageConfig) (*config.Config, error) {
	cloned, err := next.Clone()
	if err != nil {
		return nil, err
	}
	cloned.Storage = storage
	return cloned, nil
}

func mergeLoadedConfig(base *config.Config, loaded *loadedConfig) (*config.Config, error) {
	if loaded == nil || loaded.cfg == nil {
		return nil, errors.New("loaded config is nil")
	}

	merged, err := loaded.cfg.Clone()
	if err != nil {
		return nil, err
	}
	if base != nil {
		merged.Storage = base.Storage
		mergeAdminCredentials(&merged.Admin, base.Admin, loaded.adminLayer)
	}
	if err := merged.ApplyCriticalEnvOverrides(os.LookupEnv); err != nil {
		return nil, err
	}
	return merged, nil
}

func mergeAdminCredentials(dst *config.AdminConfig, fallback config.AdminConfig, layer config.AdminCredentialLayer) {
	if dst == nil {
		return
	}

	if layer.Username == nil || strings.TrimSpace(*layer.Username) == "" {
		dst.Username = fallback.Username
	}

	hasPassword := layer.Password != nil && strings.TrimSpace(*layer.Password) != ""
	hasPasswordHash := layer.PasswordHash != nil && strings.TrimSpace(*layer.PasswordHash) != ""
	if !hasPassword && !hasPasswordHash {
		dst.Password = fallback.Password
		dst.PasswordHash = fallback.PasswordHash
		return
	}
	if hasPasswordHash {
		dst.Password = ""
		return
	}
	dst.PasswordHash = ""
}

func writeConfigFile(path string, cfg *config.Config) error {
	data, err := config.EncodeYAML(cfg)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func mask(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func (s *Store) ensureBootstrapAdminPassword(cfg *config.Config) error {
	if cfg == nil || !cfg.NeedsBootstrapAdminPassword() {
		return nil
	}
	password, err := GenerateRandomPassword()
	if err != nil {
		return fmt.Errorf("generate bootstrap admin password: %w", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	cfg.Admin.Password = ""
	cfg.Admin.PasswordHash = hash
	s.initAdminPassword = password
	return nil
}
