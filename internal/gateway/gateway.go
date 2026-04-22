package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
	"jc_proxy/internal/resin"
)

type Router struct {
	vendors                map[string]*vendorGateway
	bufPool                sync.Pool
	uiFS                   http.Handler
	uiRead                 fs.FS
	consoleEnabled         bool
	adminCIDRs             []netip.Prefix
	adminTrustedProxyCIDRs []netip.Prefix
}

type vendorGateway struct {
	name                string
	provider            string
	baseURL             *url.URL
	pool                *balancer.Pool
	managedKeyCount     int
	client              *http.Client
	clientAuth          map[string]struct{}
	allowlist           map[string]struct{}
	dropHeaders         map[string]struct{}
	injectHeaders       map[string]string
	upstreamAuth        config.UpstreamAuthConfig
	upstreamBodyTimeout time.Duration
	errorPolicy         config.ErrorPolicyConfig
	rewrites            rewriteMatcher
	resinRuntime        *resin.RuntimeConfig
	keyCtrl             UpstreamKeyController
}

func New(cfg *config.Config) (*Router, error) {
	return NewWithUpstreamKeyRecords(cfg, nil, nil)
}

func NewWithUpstreamKeyRecords(cfg *config.Config, upstreamKeys map[string][]keystore.Record, keyCtrl UpstreamKeyController) (*Router, error) {
	return newRouterWithUpstreamKeyRecords(cfg, upstreamKeys, keyCtrl, nil)
}

func newRouterWithUpstreamKeyRecords(cfg *config.Config, upstreamKeys map[string][]keystore.Record, keyCtrl UpstreamKeyController, statsRegistry *runtimeStatsRegistry) (*Router, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	adminCIDRs, err := config.ParseAdminAllowedCIDRs(cfg.Admin.AllowedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse admin allowed cidrs: %w", err)
	}
	adminTrustedProxyCIDRs, err := config.ParseAdminTrustedProxyCIDRs(cfg.Admin.TrustedProxyCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse admin trusted proxy cidrs: %w", err)
	}

	vendors := make(map[string]*vendorGateway, len(cfg.Vendors))
	for name, vendor := range cfg.Vendors {
		baseURL, err := url.Parse(strings.TrimRight(vendor.Upstream.BaseURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("vendor %s parse upstream base_url: %w", name, err)
		}
		if baseURL.Scheme == "" || baseURL.Host == "" {
			return nil, fmt.Errorf("vendor %s invalid upstream base_url", name)
		}

		keyConfigs := make([]balancer.KeyConfig, 0)
		if upstreamKeys != nil {
			for _, record := range upstreamKeys[name] {
				statsHandle := (*balancer.RuntimeStatsHandle)(nil)
				if statsRegistry != nil {
					statsHandle = statsRegistry.Handle(name, record.Key, record.RuntimeStats)
				}
				keyConfigs = append(keyConfigs, balancer.KeyConfig{
					Key:           record.Key,
					Status:        record.Status,
					DisableReason: record.DisableReason,
					DisabledAt:    record.DisabledAt,
					DisabledBy:    record.DisabledBy,
					RuntimeStats:  record.RuntimeStats,
					Version:       record.Version,
					Stats:         statsHandle,
				})
			}
		}
		if len(keyConfigs) == 0 {
			for _, key := range vendor.Upstream.Keys {
				statsHandle := (*balancer.RuntimeStatsHandle)(nil)
				if statsRegistry != nil {
					statsHandle = statsRegistry.Handle(name, key, keystore.RuntimeStats{})
				}
				keyConfigs = append(keyConfigs, balancer.KeyConfig{
					Key:    key,
					Status: keystore.KeyStatusActive,
					Stats:  statsHandle,
				})
			}
		}

		pool, err := balancer.NewPoolWithConfigs(vendor.LoadBalance, keyConfigs, vendor.Backoff.Threshold, vendor.Backoff.Duration)
		if err != nil {
			return nil, fmt.Errorf("vendor %s init key pool: %w", name, err)
		}

		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          512,
			MaxIdleConnsPerHost:   128,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: vendor.Upstream.ResponseHeaderTimeout,
		}
		client := &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		clientAuthSet := make(map[string]struct{}, len(vendor.ClientAuth.Keys))
		if vendor.ClientAuth.Enabled {
			for _, key := range vendor.ClientAuth.Keys {
				clientAuthSet[key] = struct{}{}
			}
		}

		var rr *resin.RuntimeConfig
		if vendor.Resin.Enabled {
			rc, err := resin.ParseRuntime(resin.Config{
				URL:      vendor.Resin.URL,
				Platform: vendor.Resin.Platform,
				Mode:     vendor.Resin.Mode,
			})
			if err != nil {
				return nil, fmt.Errorf("vendor %s parse resin: %w", name, err)
			}
			rr = rc
		}

		vendors[name] = &vendorGateway{
			name:                name,
			provider:            config.NormalizeProvider(vendor.Provider, name),
			baseURL:             baseURL,
			pool:                pool,
			managedKeyCount:     len(keyConfigs),
			client:              client,
			clientAuth:          clientAuthSet,
			allowlist:           headerNameSet(config.ResolveClientHeaderAllowlist(vendor.ClientHeaders)),
			dropHeaders:         headerNameSet(config.ResolveClientHeaderDropList(vendor.ClientHeaders)),
			injectHeaders:       vendor.InjectedHeader,
			upstreamAuth:        vendor.UpstreamAuth,
			upstreamBodyTimeout: vendor.Upstream.BodyTimeout,
			errorPolicy:         vendor.ErrorPolicy,
			rewrites:            newRewriteMatcher(vendor.PathRewrites),
			resinRuntime:        rr,
			keyCtrl:             keyCtrl,
		}
	}

	return &Router{
		vendors: vendors,
		bufPool: sync.Pool{
			New: func() any {
				buf := make([]byte, pooledResponseCopyBufferBytes)
				return &buf
			},
		},
		uiFS:                   newUIHandler(),
		uiRead:                 newUIReadFS(),
		consoleEnabled:         cfg.Admin.Enabled,
		adminCIDRs:             adminCIDRs,
		adminTrustedProxyCIDRs: adminTrustedProxyCIDRs,
	}, nil
}

func (r *Router) VendorStats() map[string][]map[string]any {
	out := make(map[string][]map[string]any, len(r.vendors))
	for name, v := range r.vendors {
		out[name] = v.pool.Stats()
	}
	return out
}

func (r *Router) VendorStateSnapshots() map[string][]balancer.KeyState {
	out := make(map[string][]balancer.KeyState, len(r.vendors))
	for name, v := range r.vendors {
		out[name] = v.pool.Snapshot()
	}
	return out
}

func (r *Router) MergeRuntimeStatsFrom(prev *Router) {
	if r == nil || prev == nil {
		return
	}
	for name, vendor := range r.vendors {
		if vendor == nil || vendor.pool == nil {
			continue
		}
		prevVendor := prev.vendors[name]
		if prevVendor == nil || prevVendor.pool == nil {
			continue
		}
		vendor.pool.MergeRuntimeStats(prevVendor.pool.Snapshot())
	}
}
