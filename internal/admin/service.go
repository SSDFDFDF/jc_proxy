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

func (s *Service) Stats(query RuntimeStatsQuery) map[string]any {
	r := s.runtime.Snapshot()
	if r == nil {
		return map[string]any{
			"vendors": map[string][]map[string]any{},
			"meta": RuntimeStatsMeta{
				Vendor:   strings.TrimSpace(query.Vendor),
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

func (s *Service) VendorTestMeta(vendor string) (*VendorTestMetaResponse, error) {
	if err := s.requireVendor(vendor); err != nil {
		return nil, err
	}

	meta, err := s.runtime.VendorTestMeta(vendor)
	if err != nil {
		return nil, err
	}

	defaultKey, err := s.firstAvailableUpstreamKey(vendor)
	if err != nil {
		return nil, err
	}

	resp := &VendorTestMetaResponse{
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

func (s *Service) RunVendorTest(ctx context.Context, vendor string, req VendorTestRequest) (*VendorTestResponse, error) {
	if err := s.requireVendor(vendor); err != nil {
		return nil, err
	}

	selectedKey := strings.TrimSpace(req.Key)
	keySource := "manual"
	if selectedKey == "" {
		keySource = "default"
		var err error
		selectedKey, err = s.firstAvailableUpstreamKey(vendor)
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

	result, err := s.runtime.ExecuteVendorTest(ctx, vendor, gateway.VendorTestRequest{
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
		Vendor:        vendor,
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

func (s *Service) firstAvailableUpstreamKey(vendor string) (string, error) {
	records, err := s.keyStore.List(vendor)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if keystore.IsActiveStatus(record.Status) {
			return record.Key, nil
		}
	}

	cfg, err := s.store.GetConfig()
	if err != nil {
		return "", err
	}
	legacy := keystore.NormalizeKeys(cfg.Vendors[vendor].Upstream.Keys)
	if len(legacy) > 0 {
		return legacy[0], nil
	}
	return "", nil
}

func buildRuntimeStatsResponse(vendors map[string][]map[string]any, query RuntimeStatsQuery) map[string]any {
	vendor := strings.TrimSpace(query.Vendor)
	filter := normalizeRuntimeStatsFilter(query.Filter)
	keyword := strings.ToLower(strings.TrimSpace(query.Q))
	page := normalizeRuntimeStatsPage(query.Page)
	pageSize := normalizeRuntimeStatsPageSize(query.PageSize)

	if vendor == "" {
		out := make(map[string][]map[string]any, len(vendors))
		for name, items := range vendors {
			out[name] = filterRuntimeStatsItems(items, filter, keyword)
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

	items := filterRuntimeStatsItems(vendors[vendor], filter, keyword)
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
			vendor: items[start:end],
		},
		"meta": RuntimeStatsMeta{
			Vendor:   vendor,
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
