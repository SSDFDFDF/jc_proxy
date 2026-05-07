package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
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
	baseURLPrefix       string
	pool                *balancer.Pool
	managedKeyCount     int
	client              *http.Client
	clientAuth          map[string]struct{}
	allowlist           map[string]struct{}
	dropHeaders         map[string]struct{}
	injectHeaders       map[string]string
	upstreamAuth        config.UpstreamAuthConfig
	upstreamBodyTimeout time.Duration
	interimInterval     time.Duration
	errorPolicy         config.ErrorPolicyConfig
	rewrites            rewriteMatcher
	resinRuntime        *resin.RuntimeConfig
	keyCtrl             UpstreamKeyController

	// Aggregate fields
	isAggregate bool
	aggPool     *aggregatePool
}

// aggregateChildEntry holds a reference to a child vendor for aggregate routing.
type aggregateChildEntry struct {
	name     string
	vendor   *vendorGateway
	weight   int
	priority int
}

// aggregatePool implements vendor-level load balancing for aggregate providers.
// Entries are filtered to the highest-priority tier (lowest priority number)
// during construction. Within that tier, weight controls traffic distribution.
type aggregatePool struct {
	mu             sync.Mutex
	strategy       string
	entries        []aggregateChildEntry
	totalWeight    int
	currentWeights []int // smooth weighted round-robin state
	rng            *rand.Rand
}

func newAggregatePool(strategy string, entries []aggregateChildEntry) *aggregatePool {
	if len(entries) > 0 {
		// Sort by priority ascending (lower number = higher priority).
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].priority < entries[j].priority
		})
		// Keep only the highest-priority group.
		minPriority := entries[0].priority
		cut := 0
		for cut < len(entries) && entries[cut].priority == minPriority {
			cut++
		}
		entries = entries[:cut]
	}

	totalWeight := 0
	for _, e := range entries {
		totalWeight += e.weight
	}

	return &aggregatePool{
		strategy:       strategy,
		entries:        entries,
		totalWeight:    totalWeight,
		currentWeights: make([]int, len(entries)),
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (p *aggregatePool) Pick() (*aggregateChildEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) == 0 {
		return nil, false
	}
	switch p.strategy {
	case "random":
		return p.pickRandomLocked()
	case "least_used", "least_requests":
		return p.pickLeastInflightLocked()
	default:
		return p.pickRoundRobinLocked()
	}
}

// pickRoundRobinLocked uses smooth weighted round-robin.
// With weights [5, 1], the pattern distributes 5:1 over every 6 picks
// instead of bursting 5 consecutive picks to the heavier entry.
func (p *aggregatePool) pickRoundRobinLocked() (*aggregateChildEntry, bool) {
	if p.totalWeight <= 0 {
		idx := p.currentWeights[0] % len(p.entries)
		p.currentWeights[0]++
		return &p.entries[idx], true
	}
	best := 0
	for i := range p.entries {
		p.currentWeights[i] += p.entries[i].weight
		if p.currentWeights[i] > p.currentWeights[best] {
			best = i
		}
	}
	p.currentWeights[best] -= p.totalWeight
	return &p.entries[best], true
}

// pickRandomLocked uses weighted random selection.
func (p *aggregatePool) pickRandomLocked() (*aggregateChildEntry, bool) {
	if p.totalWeight <= 0 {
		return &p.entries[p.rng.Intn(len(p.entries))], true
	}
	r := p.rng.Intn(p.totalWeight)
	for i := range p.entries {
		r -= p.entries[i].weight
		if r < 0 {
			return &p.entries[i], true
		}
	}
	return &p.entries[len(p.entries)-1], true
}

func (p *aggregatePool) pickLeastInflightLocked() (*aggregateChildEntry, bool) {
	best := -1
	bestInflight := int64(-1)
	for i, entry := range p.entries {
		if entry.vendor == nil || entry.vendor.pool == nil {
			if best < 0 {
				best = i
				bestInflight = 0
			}
			continue
		}
		snap := entry.vendor.pool.Snapshot()
		var inflight int64
		for _, ks := range snap {
			inflight += int64(ks.Inflight)
		}
		if best < 0 || inflight < bestInflight {
			best = i
			bestInflight = inflight
		}
	}
	if best < 0 {
		return nil, false
	}
	return &p.entries[best], true
}

// PickAvailable selects a child vendor using the configured strategy,
// then falls back to any other entry if the preferred pick is unavailable.
// The available predicate is called under the pool lock.
// Returns nil if no entry satisfies the predicate.
func (p *aggregatePool) PickAvailable(available func(*aggregateChildEntry) bool) *aggregateChildEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) == 0 {
		return nil
	}

	// Use the configured strategy for the preferred pick.
	var preferred *aggregateChildEntry
	switch p.strategy {
	case "random":
		preferred, _ = p.pickRandomLocked()
	case "least_used", "least_requests":
		preferred, _ = p.pickLeastInflightLocked()
	default:
		preferred, _ = p.pickRoundRobinLocked()
	}
	if preferred != nil && available(preferred) {
		return preferred
	}

	// Preferred unavailable — try remaining entries.
	for i := range p.entries {
		e := &p.entries[i]
		if e == preferred {
			continue
		}
		if available(e) {
			return e
		}
	}
	return nil
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
	// First pass: create all non-aggregate vendors
	for name, vendor := range cfg.Vendors {
		if config.NormalizeProvider(vendor.Provider, name) == "aggregate" {
			continue
		}
		vg, err := buildVendorGateway(name, vendor, upstreamKeys, keyCtrl, statsRegistry)
		if err != nil {
			return nil, err
		}
		vendors[name] = vg
	}
	// Second pass: create aggregate vendors with references to child vendorGateways
	for name, vendor := range cfg.Vendors {
		if config.NormalizeProvider(vendor.Provider, name) != "aggregate" {
			continue
		}
		entries := make([]aggregateChildEntry, 0, len(vendor.Aggregate.Children))
		for _, child := range vendor.Aggregate.Children {
			childVG, ok := vendors[child.Vendor]
			if !ok {
				return nil, fmt.Errorf("vendor %s aggregate child %q not found", name, child.Vendor)
			}
			entries = append(entries, aggregateChildEntry{
				name:     child.Vendor,
				vendor:   childVG,
				weight:   child.Weight,
				priority: child.Priority,
			})
		}

		clientAuthSet := make(map[string]struct{}, len(vendor.ClientAuth.Keys))
		if vendor.ClientAuth.Enabled {
			for _, key := range vendor.ClientAuth.Keys {
				clientAuthSet[key] = struct{}{}
			}
		}

		vendors[name] = &vendorGateway{
			name:        name,
			provider:    "aggregate",
			isAggregate: true,
			clientAuth:  clientAuthSet,
			aggPool:     newAggregatePool(vendor.LoadBalance, entries),
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
		if v.pool == nil {
			continue
		}
		out[name] = v.pool.Stats()
	}
	return out
}

func (r *Router) VendorStateSnapshots() map[string][]balancer.KeyState {
	out := make(map[string][]balancer.KeyState, len(r.vendors))
	for name, v := range r.vendors {
		if v.pool == nil {
			continue
		}
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

func upstreamInterimInterval(cfg config.UpstreamConfig) time.Duration {
	if cfg.InterimResponseInterval == nil {
		return 0
	}
	return *cfg.InterimResponseInterval
}

func upstreamResponseHeaderTimeout(cfg config.UpstreamConfig) time.Duration {
	if cfg.ResponseHeaderTimeout == nil {
		return 0
	}
	return *cfg.ResponseHeaderTimeout
}

func buildVendorGateway(name string, vendor config.VendorConfig, upstreamKeys map[string][]keystore.Record, keyCtrl UpstreamKeyController, statsRegistry *runtimeStatsRegistry) (*vendorGateway, error) {
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

	pool, err := balancer.NewPoolWithConfigs(vendor.LoadBalance, keyConfigs)
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
		ResponseHeaderTimeout: upstreamResponseHeaderTimeout(vendor.Upstream),
		ReadBufferSize:        16 << 10,
		WriteBufferSize:       16 << 10,
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

	return &vendorGateway{
		name:                name,
		provider:            config.NormalizeProvider(vendor.Provider, name),
		baseURL:             baseURL,
		baseURLPrefix:       baseURL.String(),
		pool:                pool,
		managedKeyCount:     len(keyConfigs),
		client:              client,
		clientAuth:          clientAuthSet,
		allowlist:           headerNameSet(config.ResolveClientHeaderAllowlist(vendor.ClientHeaders)),
		dropHeaders:         headerNameSet(config.ResolveClientHeaderDropList(vendor.ClientHeaders)),
		injectHeaders:       vendor.InjectedHeader,
		upstreamAuth:        vendor.UpstreamAuth,
		upstreamBodyTimeout: vendor.Upstream.BodyTimeout,
		interimInterval:     upstreamInterimInterval(vendor.Upstream),
		errorPolicy:         vendor.ErrorPolicy,
		rewrites:            newRewriteMatcher(vendor.PathRewrites),
		resinRuntime:        rr,
		keyCtrl:             keyCtrl,
	}, nil
}
