package admin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
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

func (s *Service) AdminAccess() (bool, []netip.Prefix, []netip.Prefix, error) {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return false, nil, nil, err
	}
	if !cfg.Admin.Enabled {
		return false, nil, nil, nil
	}
	allowed, err := config.ParseAdminAllowedCIDRs(cfg.Admin.AllowedCIDRs)
	if err != nil {
		return false, nil, nil, err
	}
	trusted, err := config.ParseAdminTrustedProxyCIDRs(cfg.Admin.TrustedProxyCIDRs)
	if err != nil {
		return false, nil, nil, err
	}
	return true, allowed, trusted, nil
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
	for i := range cfg.Vendors {
		keys := cfg.Vendors[i].ClientAuth.Keys
		for j := range keys {
			keys[j] = mask(keys[j])
		}
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
	normalizeAdminCredentials(next, prev)
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
	if adminSessionsNeedReset(prev, next) {
		s.sessions.DeleteAll()
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

func normalizeAdminCredentials(next, prev *config.Config) {
	if next == nil || prev == nil {
		return
	}

	if next.Admin.Password == "******" {
		next.Admin.Password = prev.Admin.Password
	}
	if next.Admin.PasswordHash == "******" {
		next.Admin.PasswordHash = prev.Admin.PasswordHash
	}

	switch {
	case strings.TrimSpace(next.Admin.PasswordHash) != "":
		next.Admin.Password = ""
	case strings.TrimSpace(next.Admin.Password) != "":
		next.Admin.PasswordHash = ""
	default:
		next.Admin.Password = prev.Admin.Password
		next.Admin.PasswordHash = prev.Admin.PasswordHash
	}
}

func adminSessionsNeedReset(prev, next *config.Config) bool {
	if prev == nil || next == nil {
		return false
	}
	return prev.Admin.Enabled != next.Admin.Enabled ||
		prev.Admin.Username != next.Admin.Username ||
		prev.Admin.Password != next.Admin.Password ||
		prev.Admin.PasswordHash != next.Admin.PasswordHash
}

// CreateVendor registers a brand new vendor and mints its immutable id.
func (s *Service) CreateVendor(actor, name string, vc config.VendorConfig) (string, error) {
	name = strings.TrimSpace(name)
	if err := config.ValidateVendorName(name); err != nil {
		return "", err
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		return "", err
	}
	if _, exists := cfg.VendorByName(name); exists {
		return "", fmt.Errorf("vendor name %q already exists", name)
	}
	id, err := config.NewVendorID()
	if err != nil {
		return "", err
	}
	cfg.Vendors = append(cfg.Vendors, config.VendorEntry{ID: id, Name: name, VendorConfig: vc})
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return "", err
	}
	s.audit.Log(actor, "vendor.create", map[string]any{"vendor_id": id, "vendor": name, "provider": vc.Provider})
	return id, nil
}

// UpdateVendor replaces the configuration of an existing vendor addressed by
// its immutable id. The name is untouched here; use RenameVendor for that.
func (s *Service) UpdateVendor(actor, vendorID string, vc config.VendorConfig) error {
	vendorID = strings.TrimSpace(vendorID)
	if vendorID == "" {
		return errors.New("vendor id is required")
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	idx := cfg.VendorIndexByID(vendorID)
	if idx < 0 {
		return fmt.Errorf("vendor %q not found", vendorID)
	}
	prev := cfg.Vendors[idx]
	cfg.Vendors[idx] = config.VendorEntry{
		ID:           prev.ID,
		Name:         prev.Name,
		VendorConfig: mergeVendorConfigForAdminUpsert(prev.VendorConfig, vc),
	}
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "vendor.update", map[string]any{"vendor_id": prev.ID, "vendor": prev.Name, "provider": vc.Provider})
	return nil
}

// RenameVendor changes only the display/route name. Because every stored
// reference keys off the immutable id, nothing else moves: upstream keys,
// runtime statistics and aggregate topology are untouched. The one visible
// effect is that clients must call the new path segment.
func (s *Service) RenameVendor(actor, vendorID, newName string) error {
	vendorID = strings.TrimSpace(vendorID)
	if vendorID == "" {
		return errors.New("vendor id is required")
	}
	newName = strings.TrimSpace(newName)
	if err := config.ValidateVendorName(newName); err != nil {
		return err
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	idx := cfg.VendorIndexByID(vendorID)
	if idx < 0 {
		return fmt.Errorf("vendor %q not found", vendorID)
	}
	oldName := cfg.Vendors[idx].Name
	if oldName == newName {
		return nil
	}
	if existing := cfg.VendorIndexByName(newName); existing >= 0 {
		return fmt.Errorf("vendor name %q already used by another vendor", newName)
	}
	cfg.Vendors[idx].Name = newName
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "vendor.rename", map[string]any{"vendor_id": vendorID, "from": oldName, "to": newName})
	return nil
}

// The admin vendor editor surfaces parts of error_policy including auto_disable.
// Preserve un-submitted sub-fields on update so partial API payloads
// do not silently reset runtime behavior configured elsewhere.
func mergeVendorConfigForAdminUpsert(prev, next config.VendorConfig) config.VendorConfig {
	next.ErrorPolicy = mergeErrorPolicyForAdminUpsert(prev.ErrorPolicy, next.ErrorPolicy)
	return next
}

func mergeErrorPolicyForAdminUpsert(prev, next config.ErrorPolicyConfig) config.ErrorPolicyConfig {
	next.AutoDisable.Enabled = mergeBoolPtrForAdminUpsert(prev.AutoDisable.Enabled, next.AutoDisable.Enabled)
	if next.AutoDisable.StatusCodes == nil {
		next.AutoDisable.StatusCodes = prev.AutoDisable.StatusCodes
	}
	if next.AutoDisable.Keywords == nil {
		next.AutoDisable.Keywords = prev.AutoDisable.Keywords
	}

	next.Cooldown.RequestError = mergeCooldownRuleForAdminUpsert(prev.Cooldown.RequestError, next.Cooldown.RequestError)
	next.Cooldown.Unauthorized = mergeCooldownRuleForAdminUpsert(prev.Cooldown.Unauthorized, next.Cooldown.Unauthorized)
	next.Cooldown.PaymentRequired = mergeCooldownRuleForAdminUpsert(prev.Cooldown.PaymentRequired, next.Cooldown.PaymentRequired)
	next.Cooldown.Forbidden = mergeCooldownRuleForAdminUpsert(prev.Cooldown.Forbidden, next.Cooldown.Forbidden)
	next.Cooldown.RateLimit = mergeCooldownRuleForAdminUpsert(prev.Cooldown.RateLimit, next.Cooldown.RateLimit)
	next.Cooldown.ServerError = mergeCooldownRuleForAdminUpsert(prev.Cooldown.ServerError, next.Cooldown.ServerError)
	next.Cooldown.OpenAISlowDown = mergeCooldownRuleForAdminUpsert(prev.Cooldown.OpenAISlowDown, next.Cooldown.OpenAISlowDown)

	next.Failover.Unauthorized = mergeBoolPtrForAdminUpsert(prev.Failover.Unauthorized, next.Failover.Unauthorized)
	next.Failover.PaymentRequired = mergeBoolPtrForAdminUpsert(prev.Failover.PaymentRequired, next.Failover.PaymentRequired)
	next.Failover.Forbidden = mergeBoolPtrForAdminUpsert(prev.Failover.Forbidden, next.Failover.Forbidden)
	next.Failover.RateLimit = mergeBoolPtrForAdminUpsert(prev.Failover.RateLimit, next.Failover.RateLimit)
	next.Failover.ServerError = mergeBoolPtrForAdminUpsert(prev.Failover.ServerError, next.Failover.ServerError)

	// Masking is editable in the console, so an explicit payload wins. The
	// preservation branch remains as a safety net for API clients (and older
	// console builds) whose payload carries no masking state at all.
	if next.Masking.Enabled == nil && len(next.Masking.Rules) == 0 {
		next.Masking = prev.Masking
	}

	return next
}

func mergeBoolPtrForAdminUpsert(prev, next *bool) *bool {
	if next != nil {
		return next
	}
	return prev
}

func mergeCooldownRuleForAdminUpsert(prev, next config.ErrorCooldownRule) config.ErrorCooldownRule {
	if next.Enabled != nil || next.Duration != 0 {
		return next
	}
	return prev
}

func (s *Service) DeleteVendor(actor, vendorID string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	entry, ok := cfg.VendorByID(vendorID)
	if !ok {
		return fmt.Errorf("vendor %q not found", vendorID)
	}
	// Refuse to strand an aggregate that still routes to this vendor.
	for i := range cfg.Vendors {
		if cfg.Vendors[i].ID == entry.ID {
			continue
		}
		for _, child := range cfg.Vendors[i].Aggregate.Children {
			if child.VendorID == entry.ID {
				return fmt.Errorf("vendor %q is still referenced by aggregate vendor %q", entry.Name, cfg.Vendors[i].Name)
			}
		}
	}
	cfg.DeleteVendorByID(entry.ID)
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	if err := s.keyStore.DeleteVendor(entry.ID); err != nil {
		return err
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "vendor.delete", map[string]any{"vendor_id": entry.ID, "vendor": entry.Name})
	return nil
}

func (s *Service) AddUpstreamKey(actor, vendor, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	return s.AddUpstreamKeys(actor, vendor, []string{key})
}

func (s *Service) AddUpstreamKeys(actor, vendor string, keys []string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	keys = keystore.NormalizeKeys(keys)
	if len(keys) == 0 {
		return errors.New("key is empty")
	}
	added, err := s.keyStore.Append(vendor, keys)
	if err != nil {
		return err
	}
	if added == 0 {
		return errors.New("duplicate key")
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.add", map[string]any{"vendor": vendor, "count": added})
	return nil
}

func (s *Service) DeleteUpstreamKey(actor, vendor, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	return s.DeleteUpstreamKeys(actor, vendor, []string{key})
}

func (s *Service) DeleteUpstreamKeys(actor, vendor string, keys []string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	keys = keystore.NormalizeKeys(keys)
	if len(keys) == 0 {
		return errors.New("key is empty")
	}
	removed, err := s.keyStore.Delete(vendor, keys)
	if err != nil {
		return err
	}
	if removed == 0 {
		return errors.New("key not found")
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.delete", map[string]any{"vendor": vendor, "count": removed})
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
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	return s.SetUpstreamKeyStatus(actor, vendor, []string{key}, status, reason)
}

func (s *Service) EnableUpstreamKey(actor, vendor, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key is empty")
	}
	return s.SetUpstreamKeyStatus(actor, vendor, []string{key}, keystore.KeyStatusActive, "")
}

func (s *Service) RecoverUpstreamKeys(actor, vendor string, keys []string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	keys = keystore.NormalizeKeys(keys)
	if len(keys) == 0 {
		return errors.New("key is empty")
	}
	for _, key := range keys {
		if err := s.keyStore.SetStatus(vendor, key, keystore.KeyStatusActive, "", actor); err != nil {
			return err
		}
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	for _, key := range keys {
		s.runtime.RecoverUpstreamKey(vendor, key)
	}
	s.audit.Log(actor, "upstream_key.recover", map[string]any{"vendor": vendor, "count": len(keys)})
	return nil
}

func (s *Service) SetUpstreamKeyStatus(actor, vendor string, keys []string, status, reason string) error {
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	keys = keystore.NormalizeKeys(keys)
	if len(keys) == 0 {
		return errors.New("key is empty")
	}
	status = keystore.NormalizeStatus(status)
	if status == keystore.KeyStatusActive {
		reason = ""
	}
	for _, key := range keys {
		if err := s.keyStore.SetStatus(vendor, key, status, reason, actor); err != nil {
			return err
		}
	}
	if err := s.runtime.RefreshKeys(); err != nil {
		return err
	}
	action := "upstream_key.enable"
	if !keystore.IsActiveStatus(status) {
		action = "upstream_key.disable"
	}
	s.audit.Log(actor, action, map[string]any{"vendor": vendor, "status": status, "count": len(keys)})
	return nil
}

func (s *Service) SetUpstreamKeyRemark(actor, vendor, key, remark string) error {
	vendor = strings.TrimSpace(vendor)
	key = strings.TrimSpace(key)
	remark = strings.TrimSpace(remark)
	if err := s.requireVendor(vendor); err != nil {
		return err
	}
	if key == "" {
		return errors.New("key is required")
	}
	if len([]rune(remark)) > 500 {
		return errors.New("remark must not exceed 500 characters")
	}
	remarkStore, ok := s.keyStore.(keystore.RemarkStore)
	if !ok {
		return errors.New("upstream key store does not support remarks")
	}
	if err := remarkStore.SetRemark(vendor, key, remark); err != nil {
		return err
	}
	s.audit.Log(actor, "upstream_key.remark", map[string]any{"vendor": vendor, "key_id": keystore.KeyID(key)})
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
	vendorNames := make(map[string]string, len(cfg.Vendors))
	for i := range cfg.Vendors {
		vendorSet[cfg.Vendors[i].ID] = struct{}{}
		vendorNames[cfg.Vendors[i].ID] = cfg.Vendors[i].Name
	}
	// Partitions with no matching config entry are orphans left behind by a
	// failed delete; surface them so an operator can clean them up.
	for vendorID := range all {
		vendorSet[vendorID] = struct{}{}
	}

	vendors := make([]string, 0, len(vendorSet))
	for vendorID := range vendorSet {
		vendors = append(vendors, vendorID)
	}
	sort.Slice(vendors, func(i, j int) bool {
		ni, nj := vendorNames[vendors[i]], vendorNames[vendors[j]]
		if ni != nj {
			return ni < nj
		}
		return vendors[i] < vendors[j]
	})

	resp := &UpstreamKeysResponse{
		Storage: s.keyStore.Info(),
		Vendors: make([]UpstreamKeyVendorSummary, 0, len(vendors)),
		Items:   make(map[string][]UpstreamKeyRecordResponse, len(vendors)),
	}
	for _, vendorID := range vendors {
		records := all[vendorID]
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
				KeyID:         keystore.KeyID(record.Key),
				Masked:        mask(record.Key),
				Remark:        record.Remark,
				Status:        record.Status,
				DisableReason: record.DisableReason,
				DisabledAt:    disabledAt,
				DisabledBy:    record.DisabledBy,
				CreatedAt:     record.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt:     record.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
		name, configured := vendorNames[vendorID]
		if !configured {
			name = vendorID
		}
		resp.Vendors = append(resp.Vendors, UpstreamKeyVendorSummary{
			VendorID:      vendorID,
			Vendor:        name,
			Count:         len(items),
			ActiveCount:   activeCount,
			DisabledCount: disabledCount,
			Configured:    configured,
		})
		resp.Items[vendorID] = items
	}
	return resp, nil
}

func (s *Service) AddClientKey(actor, vendorID, key string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	idx := cfg.VendorIndexByID(vendorID)
	if idx < 0 {
		return fmt.Errorf("vendor %q not found", vendorID)
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("key is empty")
	}
	for _, existing := range cfg.Vendors[idx].ClientAuth.Keys {
		if existing == key {
			return errors.New("duplicate key")
		}
	}
	cfg.Vendors[idx].ClientAuth.Enabled = true
	cfg.Vendors[idx].ClientAuth.Keys = append(cfg.Vendors[idx].ClientAuth.Keys, key)
	name := cfg.Vendors[idx].Name
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "client_key.add", map[string]any{"vendor_id": vendorID, "vendor": name})
	return nil
}

func (s *Service) DeleteClientKey(actor, vendorID, key string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	idx := cfg.VendorIndexByID(vendorID)
	if idx < 0 {
		return fmt.Errorf("vendor %q not found", vendorID)
	}
	current := cfg.Vendors[idx].ClientAuth.Keys
	next := make([]string, 0, len(current))
	found := false
	for _, k := range current {
		if k == key {
			found = true
			continue
		}
		next = append(next, k)
	}
	if !found {
		return errors.New("key not found")
	}
	cfg.Vendors[idx].ClientAuth.Keys = next
	if len(next) == 0 {
		cfg.Vendors[idx].ClientAuth.Enabled = false
	}
	name := cfg.Vendors[idx].Name
	if err := s.UpdateConfig(actor, cfg); err != nil {
		return err
	}
	s.audit.Log(actor, "client_key.delete", map[string]any{"vendor_id": vendorID, "vendor": name})
	return nil
}

func (s *Service) Stats(query RuntimeStatsQuery) map[string]any {
	r := s.runtime.Snapshot()
	if r == nil {
		return map[string]any{
			"vendors": map[string][]map[string]any{},
			"meta": RuntimeStatsMeta{
				VendorID: strings.TrimSpace(query.VendorID),
				Filter:   normalizeRuntimeStatsFilter(query.Filter),
				Q:        strings.TrimSpace(query.Q),
				Page:     normalizeRuntimeStatsPage(query.Page),
				PageSize: normalizeRuntimeStatsPageSize(query.PageSize),
				Total:    0,
			},
		}
	}
	return buildRuntimeStatsResponse(r.VendorStats(), query)
}

func (s *Service) VendorTestMeta(vendorID string) (*VendorTestMetaResponse, error) {
	if err := s.requireVendor(vendorID); err != nil {
		return nil, err
	}

	meta, err := s.runtime.VendorTestMeta(vendorID)
	if err != nil {
		return nil, err
	}

	defaultKey, err := s.firstAvailableUpstreamKey(vendorID)
	if err != nil {
		return nil, err
	}

	resp := &VendorTestMetaResponse{
		VendorID:            meta.VendorID,
		Vendor:              meta.Vendor,
		Provider:            meta.Provider,
		BaseURL:             meta.BaseURL,
		DefaultKeyAvailable: defaultKey != "",
		ModelEndpoints:      append([]string(nil), meta.ModelEndpoints...),
		RequestPresets:      make([]VendorTestPresetResponse, 0, len(meta.RequestPresets)),
	}
	if defaultKey != "" {
		resp.DefaultKeyMasked = mask(defaultKey)
	}
	for _, preset := range meta.RequestPresets {
		resp.RequestPresets = append(resp.RequestPresets, VendorTestPresetResponse{
			Label:    preset.Label,
			Method:   preset.Method,
			Endpoint: preset.Endpoint,
			Body:     preset.Body,
		})
	}
	return resp, nil
}

func (s *Service) RunVendorTest(ctx context.Context, vendorID string, req VendorTestRequest) (*VendorTestResponse, error) {
	if err := s.requireVendor(vendorID); err != nil {
		return nil, err
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		return nil, err
	}
	vendorName, _ := cfg.VendorNameByID(vendorID)

	selectedKey := strings.TrimSpace(req.Key)
	keySource := "manual"
	if selectedKey == "" {
		keySource = "default"
		var err error
		selectedKey, err = s.firstAvailableUpstreamKey(vendorID)
		if err != nil {
			return nil, err
		}
		if selectedKey == "" {
			keySource = "none"
		}
	}

	headers := make(map[string]string, len(req.Headers))
	for _, row := range req.Headers {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		headers[key] = row.Value
	}

	result, err := s.runtime.ExecuteVendorTest(ctx, vendorID, gateway.VendorTestRequest{
		BaseURL:  req.BaseURL,
		Method:   req.Method,
		Endpoint: req.Endpoint,
		Body:     req.Body,
		Key:      selectedKey,
		Headers:  headers,
	})
	if err != nil {
		return nil, err
	}

	resp := &VendorTestResponse{
		VendorID:      vendorID,
		Vendor:        vendorName,
		Provider:      result.Provider,
		BaseURL:       result.BaseURL,
		Endpoint:      result.Endpoint,
		ResolvedURL:   result.ResolvedURL,
		Method:        result.Method,
		StatusCode:    result.StatusCode,
		Headers:       result.Headers,
		Body:          result.Body,
		Truncated:     result.Truncated,
		DurationMS:    result.DurationMS,
		UsedKeySource: keySource,
	}
	if selectedKey != "" {
		resp.UsedKeyMasked = mask(selectedKey)
	}
	return resp, nil
}

func (s *Service) requireVendor(vendorID string) error {
	cfg, err := s.store.GetConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.VendorByID(vendorID); !ok {
		return fmt.Errorf("vendor %q not found", vendorID)
	}
	return nil
}

func (s *Service) firstAvailableUpstreamKey(vendorID string) (string, error) {
	records, err := s.keyStore.List(vendorID)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if keystore.IsActiveStatus(record.Status) {
			return record.Key, nil
		}
	}
	return "", nil
}

func buildRuntimeStatsResponse(vendors map[string][]map[string]any, query RuntimeStatsQuery) map[string]any {
	vendorID := strings.TrimSpace(query.VendorID)
	filter := normalizeRuntimeStatsFilter(query.Filter)
	keyword := strings.ToLower(strings.TrimSpace(query.Q))
	page := normalizeRuntimeStatsPage(query.Page)
	pageSize := normalizeRuntimeStatsPageSize(query.PageSize)

	if vendorID == "" {
		out := make(map[string][]map[string]any, len(vendors))
		for id, items := range vendors {
			out[id] = filterRuntimeStatsItems(items, filter, keyword)
		}
		return map[string]any{
			"vendors": out,
			"meta": RuntimeStatsMeta{
				Filter:   filter,
				Q:        strings.TrimSpace(query.Q),
				Page:     1,
				PageSize: pageSize,
				Total:    0,
			},
		}
	}

	items := filterRuntimeStatsItems(vendors[vendorID], filter, keyword)
	total := len(items)
	if total == 0 {
		page = 1
	} else {
		lastPage := (total-1)/pageSize + 1
		if page > lastPage {
			page = lastPage
		}
	}

	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return map[string]any{
		"vendors": map[string][]map[string]any{
			vendorID: items[start:end],
		},
		"meta": RuntimeStatsMeta{
			VendorID: vendorID,
			Filter:   filter,
			Q:        strings.TrimSpace(query.Q),
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	}
}

func filterRuntimeStatsItems(items []map[string]any, filter, keyword string) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if !matchesRuntimeStatsFilter(item, filter) {
			continue
		}
		if keyword != "" && !matchesRuntimeStatsKeyword(item, keyword) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func matchesRuntimeStatsFilter(item map[string]any, filter string) bool {
	status := strings.TrimSpace(stringValue(item["status"]))
	backoff := intValue(item["backoff_remaining_seconds"])
	inflight := intValue(item["inflight"])
	failures := intValue(item["failures"])
	unauthorized := intValue(item["unauthorized_count"])
	forbidden := intValue(item["forbidden_count"])
	rateLimit := intValue(item["rate_limit_count"])
	otherErrors := intValue(item["other_error_count"])
	hasError := strings.TrimSpace(stringValue(item["disable_reason"])) != "" || strings.TrimSpace(stringValue(item["last_error"])) != ""
	hasIssue := status != "" && status != keystore.KeyStatusActive
	hasIssue = hasIssue || backoff > 0 || failures > 0 || unauthorized > 0 || forbidden > 0 || rateLimit > 0 || otherErrors > 0 || hasError

	switch filter {
	case "active":
		return status == keystore.KeyStatusActive
	case "disabled":
		return status == keystore.KeyStatusDisabledAuto || status == keystore.KeyStatusDisabledManual
	case "backoff":
		return backoff > 0
	case "issues":
		return hasIssue
	case "inflight":
		return inflight > 0
	default:
		return true
	}
}

func matchesRuntimeStatsKeyword(item map[string]any, keyword string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		stringValue(item["key_masked"]),
		stringValue(item["status"]),
		stringValue(item["disable_reason"]),
		stringValue(item["disabled_by"]),
		stringValue(item["last_error"]),
	}, " "))
	return strings.Contains(haystack, keyword)
}

func normalizeRuntimeStatsFilter(filter string) string {
	switch strings.TrimSpace(strings.ToLower(filter)) {
	case "active", "disabled", "backoff", "issues", "inflight":
		return strings.TrimSpace(strings.ToLower(filter))
	default:
		return "all"
	}
}

func normalizeRuntimeStatsPage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func normalizeRuntimeStatsPageSize(pageSize int) int {
	switch {
	case pageSize <= 0:
		return 50
	case pageSize > 200:
		return 200
	default:
		return pageSize
	}
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}
