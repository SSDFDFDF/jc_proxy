package admin

import (
	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Username  string `json:"username"`
}

type VendorUpsertRequest struct {
	Vendor string              `json:"vendor"`
	Config config.VendorConfig `json:"config"`
}

type VendorPatchRequest struct {
	LoadBalance  *string                    `json:"load_balance,omitempty"`
	PathRewrites map[string]string          `json:"path_rewrites,omitempty"`
	InjectHeader map[string]string          `json:"inject_headers,omitempty"`
	Allowlist    []string                   `json:"allowlist,omitempty"`
	UpstreamAuth *config.UpstreamAuthConfig `json:"upstream_auth,omitempty"`
	ErrorPolicy  *config.ErrorPolicyConfig  `json:"error_policy,omitempty"`
}

type KeyCreateRequest struct {
	Key string `json:"key"`
}

type KeyDeleteRequest struct {
	Key string `json:"key"`
}

type UpstreamKeysReplaceRequest struct {
	Keys []string `json:"keys"`
}

type UpstreamKeysDeleteRequest struct {
	Keys []string `json:"keys"`
}

type UpstreamKeyStatusRequest struct {
	Key    string `json:"key"`
	Reason string `json:"reason,omitempty"`
}

type UpstreamKeyRecordResponse struct {
	Key           string `json:"key"`
	Masked        string `json:"masked"`
	Status        string `json:"status"`
	DisableReason string `json:"disable_reason,omitempty"`
	DisabledAt    string `json:"disabled_at,omitempty"`
	DisabledBy    string `json:"disabled_by,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type UpstreamKeyVendorSummary struct {
	Vendor        string `json:"vendor"`
	Count         int    `json:"count"`
	ActiveCount   int    `json:"active_count"`
	DisabledCount int    `json:"disabled_count"`
	Configured    bool   `json:"configured"`
}

type UpstreamKeysResponse struct {
	Storage keystore.Info                          `json:"storage"`
	Vendors []UpstreamKeyVendorSummary             `json:"vendors"`
	Items   map[string][]UpstreamKeyRecordResponse `json:"items"`
}

type RuntimeStatsQuery struct {
	Vendor   string `json:"vendor,omitempty"`
	Filter   string `json:"filter,omitempty"`
	Q        string `json:"q,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}

type RuntimeStatsMeta struct {
	Vendor   string `json:"vendor,omitempty"`
	Filter   string `json:"filter,omitempty"`
	Q        string `json:"q,omitempty"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Total    int    `json:"total"`
}

type VendorTestPresetResponse struct {
	Label    string `json:"label"`
	Method   string `json:"method"`
	Endpoint string `json:"endpoint"`
	Body     string `json:"body,omitempty"`
}

type VendorTestMetaResponse struct {
	Vendor              string                     `json:"vendor"`
	Provider            string                     `json:"provider"`
	BaseURL             string                     `json:"base_url"`
	DefaultKeyMasked    string                     `json:"default_key_masked,omitempty"`
	DefaultKeyAvailable bool                       `json:"default_key_available"`
	ModelEndpoints      []string                   `json:"model_endpoints"`
	RequestPresets      []VendorTestPresetResponse `json:"request_presets"`
}

type VendorTestRequest struct {
	BaseURL  string   `json:"base_url,omitempty"`
	Method   string   `json:"method,omitempty"`
	Endpoint string   `json:"endpoint"`
	Body     string   `json:"body,omitempty"`
	Key      string   `json:"key,omitempty"`
	Headers  []KVPair `json:"headers,omitempty"`
}

type VendorTestResponse struct {
	Vendor        string              `json:"vendor"`
	Provider      string              `json:"provider"`
	BaseURL       string              `json:"base_url"`
	Endpoint      string              `json:"endpoint"`
	ResolvedURL   string              `json:"resolved_url"`
	Method        string              `json:"method"`
	StatusCode    int                 `json:"status_code"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"`
	Truncated     bool                `json:"truncated"`
	DurationMS    int64               `json:"duration_ms"`
	UsedKeyMasked string              `json:"used_key_masked,omitempty"`
	UsedKeySource string              `json:"used_key_source"`
}

type KVPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
