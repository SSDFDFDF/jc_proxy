package admin

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

type Service struct {
	store    *Store
	runtime  *gateway.Runtime
	keyStore keystore.Store
	sessions *SessionManager
	audit    *AuditLogger
}

func NewService(store *Store, runtime *gateway.Runtime, keyStore keystore.Store, sessions *SessionManager, audit *AuditLogger) *Service {
	return &Service{store: store, runtime: runtime, keyStore: keyStore, sessions: sessions, audit: audit}
}

func (s *Service) Login(username, password string) (token string, expiresAt string, err error) {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return "", "", err
	}
	if !cfg.Admin.Enabled {
		return "", "", errors.New("admin disabled")
	}
	if username != cfg.Admin.Username {
		return "", "", errors.New("invalid username or password")
	}
	if !cfg.Admin.HasCredentials() {
		return "", "", errors.New("admin credentials not initialized")
	}
	ok := false
	if strings.TrimSpace(cfg.Admin.PasswordHash) != "" {
		ok = VerifyPassword(password, cfg.Admin.PasswordHash)
	} else {
		ok = password == cfg.Admin.Password
	}
	if !ok {
		return "", "", errors.New("invalid username or password")
	}
	t, exp := s.sessions.Create(username)
	s.audit.Log(username, "admin.login", nil)
	return t, exp.UTC().Format("2006-01-02T15:04:05Z"), nil
}

func (s *Service) AdminAccessPrefixes() (bool, []netip.Prefix, error) {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return false, nil, err
	}
	if !cfg.Admin.Enabled {
		return false, nil, nil
	}
	prefixes, err := config.ParseAdminAllowedCIDRs(cfg.Admin.AllowedCIDRs)
	if err != nil {
		return false, nil, err
	}
	return true, prefixes, nil
}

func (s *Service) Logout(token, actor string) {
	s.sessions.Delete(token)
	s.audit.Log(actor, "admin.logout", nil)
}

func (s *Service) GetConfigMasked() (*config.Config, error) {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return nil, err
	}
	cfg.Admin.Password = "******"
	if cfg.Admin.PasswordHash != "" {
		cfg.Admin.PasswordHash = "******"
	}
	for name, v := range cfg.Vendors {
		for i := range v.ClientAuth.Keys {
			v.ClientAuth.Keys[i] = mask(v.ClientAuth.Keys[i])
		}
		cfg.Vendors[name] = v
	}
	return cfg, nil
}

func (s *Service) GetConfigRaw() (*config.Config, error) {
	return s.store.GetConfig()
}

func (s *Service) UpdateConfig(actor string, next *config.Config) error {
	if next == nil {
		return errors.New("config is nil")
	}
	prev, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	if next.Admin.Password == "******" || strings.TrimSpace(next.Admin.Password) == "" {
		next.Admin.Password = prev.Admin.Password
	}
	if next.Admin.PasswordHash == "******" || strings.TrimSpace(next.Admin.PasswordHash) == "" {
		next.Admin.PasswordHash = prev.Admin.PasswordHash
	}
	next.Storage = prev.Storage
	if err := next.PrepareAndValidate(); err != nil {
		return err
	}
	if err := s.runtime.Update(next); err != nil {
		return err
	}
	if err := s.store.UpdateConfig(next); err != nil {
		return err
	}
	s.audit.Log(actor, "config.update", map[string]any{"vendors": len(next.Vendors)})
	return nil
}

func (s *Service) RotatePassword(actor, plaintext string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	hash, err := HashPassword(plaintext)
	if err != nil {
		return err
	}
	cfg.Admin.PasswordHash = hash
	cfg.Admin.Password = ""
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "admin.password.rotate", nil)
	return nil
}

func (s *Service) UpsertVendor(actor, vendor string, vc config.VendorConfig) error {
	vendor = strings.TrimSpace(vendor)
	if vendor == "" {
		return errors.New("vendor is required")
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	if cfg.Vendors == nil {
		cfg.Vendors = map[string]config.VendorConfig{}
	}
	legacyKeys := keystore.NormalizeKeys(vc.Upstream.Keys)
	vc.Upstream.Keys = nil
	cfg.Vendors[vendor] = vc
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	if len(legacyKeys) > 0 {
		if _, err := s.keyStore.Append(vendor, legacyKeys); err != nil {
			return err
		}
		if err := s.runtime.RefreshKeys(); err != nil {
			return err
		}
	}
	s.audit.Log(actor, "vendor.upsert", map[string]any{"vendor": vendor, "legacy_keys_imported": len(legacyKeys)})
	return nil
}

func (s *Service) DeleteVendor(actor, vendor string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Vendors[vendor]; !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	delete(cfg.Vendors, vendor)
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	if err := s.keyStore.DeleteVendor(vendor); err != nil {
		return err
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "vendor.delete", map[string]any{"vendor": vendor})
	return nil
}

func (s *Service) AddUpstreamKey(actor, vendor, key string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	added, err := s.keyStore.Append(vendor, []string{key})
	if err != nil {
		return err
	}
	if added == 0 {
		return errors.New("duplicate key")
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.add", map[string]any{"vendor": vendor})
	return nil
}

func (s *Service) DeleteUpstreamKey(actor, vendor, key string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	removed, err := s.keyStore.Delete(vendor, []string{key})
	if err != nil {
		return err
	}
	if removed == 0 {
		return errors.New("key not found")
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.delete", map[string]any{"vendor": vendor})
	return nil
}

func (s *Service) ReplaceUpstreamKeys(actor, vendor string, keys []string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	keys = keystore.NormalizeKeys(keys)
	if err := s.keyStore.Replace(vendor, keys); err != nil {
		return err
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.replace", map[string]any{"vendor": vendor, "count": len(keys)})
	return nil
}

func (s *Service) DisableUpstreamKey(actor, vendor, key, reason string, status string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	if err := s.keyStore.SetStatus(vendor, key, status, reason, actor); err != nil {
		return err
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.disable", map[string]any{"vendor": vendor, "status": status})
	return nil
}

func (s *Service) EnableUpstreamKey(actor, vendor, key string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	if err := s.keyStore.SetStatus(vendor, key, keystore.KeyStatusActive, "", actor); err != nil {
		return err
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.enable", map[string]any{"vendor": vendor})
	return nil
}

func (s *Service) ListUpstreamKeys() (*UpstreamKeysResponse, error) {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return nil, err
	}
	all, err := s.keyStore.ListAll()
	if err != nil {
		return nil, err
	}

	vendorSet := make(map[string]struct{}, len(cfg.Vendors)+len(all))
	for vendor := range cfg.Vendors {
		vendorSet[vendor] = struct{}{}
	}
	for vendor := range all {
		vendorSet[vendor] = struct{}{}
	}

	vendors := make([]string, 0, len(vendorSet))
	for vendor := range vendorSet {
		vendors = append(vendors, vendor)
	}
	sort.Strings(vendors)

	resp := &UpstreamKeysResponse{
		Storage: s.keyStore.Info(),
		Vendors: make([]UpstreamKeyVendorSummary, 0, len(vendors)),
		Items:   make(map[string][]UpstreamKeyRecordResponse, len(vendors)),
	}
	for _, vendor := range vendors {
		records := all[vendor]
		sort.Slice(records, func(i, j int) bool {
			return records[i].Key < records[j].Key
		})
		items := make([]UpstreamKeyRecordResponse, 0, len(records))
		activeCount := 0
		disabledCount := 0
		for _, record := range records {
			disabledAt := ""
			if record.DisabledAt != nil {
				disabledAt = record.DisabledAt.UTC().Format(time.RFC3339)
			}
			if keystore.IsActiveStatus(record.Status) {
				activeCount++
			} else {
				disabledCount++
			}
			items = append(items, UpstreamKeyRecordResponse{
				Key:           record.Key,
				Masked:        mask(record.Key),
				Status:        record.Status,
				DisableReason: record.DisableReason,
				DisabledAt:    disabledAt,
				DisabledBy:    record.DisabledBy,
				CreatedAt:     record.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt:     record.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
		resp.Vendors = append(resp.Vendors, UpstreamKeyVendorSummary{
			Vendor:        vendor,
			Count:         len(items),
			ActiveCount:   activeCount,
			DisabledCount: disabledCount,
			Configured:    cfg.Vendors[vendor].Upstream.BaseURL != "",
		})
		resp.Items[vendor] = items
	}
	return resp, nil
}

func (s *Service) AddClientKey(actor, vendor, key string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("key is empty")
	}
	for _, existing := range vc.ClientAuth.Keys {
		if existing == key {
			return errors.New("duplicate key")
		}
	}
	vc.ClientAuth.Enabled = true
	vc.ClientAuth.Keys = append(vc.ClientAuth.Keys, key)
	cfg.Vendors[vendor] = vc
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "client_key.add", map[string]any{"vendor": vendor})
	return nil
}

func (s *Service) DeleteClientKey(actor, vendor, key string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	next := make([]string, 0, len(vc.ClientAuth.Keys))
	found := false
	for _, k := range vc.ClientAuth.Keys {
		if k == key {
			found = true
			continue
		}
		next = append(next, k)
	}
	if !found {
		return errors.New("key not found")
	}
	vc.ClientAuth.Keys = next
	if len(next) == 0 {
		vc.ClientAuth.Enabled = false
	}
	cfg.Vendors[vendor] = vc
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "client_key.delete", map[string]any{"vendor": vendor})
	return nil
}

func (s *Service) Stats() map[string]any {
	r := s.runtime.Snapshot()
	if r == nil {
		return map[string]any{"vendors": map[string]any{}}
	}
	return map[string]any{"vendors": r.VendorStats()}
}

func (s *Service) requireVendor(vendor string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Vendors[vendor]; !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	return nil
}
