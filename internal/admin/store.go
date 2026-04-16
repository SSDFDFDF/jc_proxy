package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"jc_proxy/internal/config"
)

type configBackend interface {
	Load() (*config.Config, error)
	Save(*config.Config) error
	Close() error
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
	if configPath == "" {
		return nil, errors.New("config path required")
	}
	if bootstrap == nil {
		return nil, errors.New("bootstrap config is nil")
	}

	s := &Store{
		path:      configPath,
		bootstrap: bootstrap.Storage,
	}

	effective, err := bootstrap.Clone()
	if err != nil {
		return nil, err
	}
	seedRemote := false

	if bootstrap.Storage.Config.Driver == "pgsql" {
		backend, err := newPGConfigBackend(bootstrap.Storage.Config.PGSQL)
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
			loaded.Storage = bootstrap.Storage
			effective = loaded
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
	if s.useRemote && (seedRemote || s.initAdminPassword != "") {
		if err := s.remote.Save(s.cfg); err != nil {
			_ = s.remote.Close()
			return nil, err
		}
	}
	if s.initAdminPassword != "" {
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
	if err := writeConfigFile(s.path, sanitized); err != nil {
		return err
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
		for k, v := range cfg.Vendors {
			if v.ClientAuth.Enabled {
				for i := range v.ClientAuth.Keys {
					v.ClientAuth.Keys[i] = mask(v.ClientAuth.Keys[i])
				}
			}
			cfg.Vendors[k] = v
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
	cloned.StripExternalizedData()
	return cloned, nil
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
