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
