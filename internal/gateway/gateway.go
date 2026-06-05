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
	aggRetry    config.AggregateRetryConfig
}

// aggregateChildEntry holds a reference to a child vendor for aggregate routing.
type aggregateChildEntry struct {
	name     string
	vendor   *vendorGateway
	weight   int
	priority int
}

// aggregatePool implements vendor-level load balancing for aggregate providers.
// Lower priority numbers are preferred. Selection is made within the highest
// priority tier that currently has an available child, so lower tiers can take
// over when all higher-priority children are disabled or cooling down.
type aggregatePool struct {
	mu             sync.Mutex
	strategy       string
	entries        []aggregateChildEntry
	currentWeights []int // smooth weighted round-robin state
	rng            *rand.Rand
}

func newAggregatePool(strategy string, entries []aggregateChildEntry) *aggregatePool {
	entries = append([]aggregateChildEntry(nil), entries...)
	for i := range entries {
		if entries[i].weight <= 0 {
			entries[i].weight = 1
		}
	}
	if len(entries) > 0 {
		// Sort by priority ascending (lower number = higher priority).
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].priority < entries[j].priority
		})
	}

	return &aggregatePool{
		strategy:       strategy,
		entries:        entries,
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
	entry := p.pickAvailableLocked(func(*aggregateChildEntry) bool { return true }, nil, true)
	return entry, entry != nil
}

// pickRoundRobinLocked uses smooth weighted round-robin.
// With weights [5, 1], the pattern distributes 5:1 over every 6 picks
// instead of bursting 5 consecutive picks to the heavier entry.
func (p *aggregatePool) pickRoundRobinLocked(indexes []int) (*aggregateChildEntry, bool) {
	if len(indexes) == 0 {
		return nil, false
	}
	totalWeight := 0
	for _, i := range indexes {
		totalWeight += p.entries[i].weight
	}
	if totalWeight <= 0 {
		return &p.entries[indexes[0]], true
	}

	best := indexes[0]
	for _, i := range indexes {
		p.currentWeights[i] += p.entries[i].weight
		if p.currentWeights[i] > p.currentWeights[best] {
			best = i
		}
	}
	p.currentWeights[best] -= totalWeight
	return &p.entries[best], true
}

// pickRandomLocked uses weighted random selection.
func (p *aggregatePool) pickRandomLocked(indexes []int) (*aggregateChildEntry, bool) {
	if len(indexes) == 0 {
		return nil, false
	}
	totalWeight := 0
	for _, i := range indexes {
		totalWeight += p.entries[i].weight
	}
	if totalWeight <= 0 {
		return &p.entries[indexes[p.rng.Intn(len(indexes))]], true
	}
	r := p.rng.Intn(totalWeight)
	for _, i := range indexes {
		r -= p.entries[i].weight
		if r < 0 {
			return &p.entries[i], true
		}
	}
	return &p.entries[indexes[len(indexes)-1]], true
}

func (p *aggregatePool) pickLeastLocked(indexes []int, includeRequests bool) (*aggregateChildEntry, bool) {
	if len(indexes) == 0 {
		return nil, false
	}
	best := indexes[0]
	bestPrimary, bestSecondary := aggregateChildLoadScore(p.entries[best], includeRequests)
	for _, i := range indexes[1:] {
		primary, secondary := aggregateChildLoadScore(p.entries[i], includeRequests)
		switch compareWeightedScore(primary, p.entries[i].weight, bestPrimary, p.entries[best].weight) {
		case -1:
			best = i
			bestPrimary, bestSecondary = primary, secondary
			continue
		case 1:
			continue
		}
		switch compareWeightedScore(secondary, p.entries[i].weight, bestSecondary, p.entries[best].weight) {
		case -1:
			best = i
			bestPrimary, bestSecondary = primary, secondary
		case 0:
			if p.entries[i].weight > p.entries[best].weight || (p.entries[i].weight == p.entries[best].weight && p.entries[i].name < p.entries[best].name) {
				best = i
				bestPrimary, bestSecondary = primary, secondary
			}
		}
	}
	return &p.entries[best], true
}

func aggregateChildLoadScore(entry aggregateChildEntry, includeRequests bool) (primary int64, secondary int64) {
	if entry.vendor == nil || entry.vendor.pool == nil {
		return 0, 0
	}
	snap := entry.vendor.pool.Snapshot()
	var inflight int64
	var totalRequests int64
	for _, ks := range snap {
		inflight += int64(ks.Inflight)
		totalRequests += int64(ks.TotalRequests)
	}
	if includeRequests {
		return totalRequests + inflight, inflight
	}
	return inflight, totalRequests
}

func compareWeightedScore(leftScore int64, leftWeight int, rightScore int64, rightWeight int) int {
	if leftWeight <= 0 {
		leftWeight = 1
	}
	if rightWeight <= 0 {
		rightWeight = 1
	}
	left := leftScore * int64(rightWeight)
	right := rightScore * int64(leftWeight)
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// PickAvailable selects a child vendor from the highest-priority available tier
// using the configured strategy.
// The available predicate is called under the pool lock.
// exclude names child vendors to skip (used for aggregate retry).
// Returns nil if no entry satisfies the predicate.
func (p *aggregatePool) PickAvailable(available func(*aggregateChildEntry) bool, exclude map[string]struct{}) *aggregateChildEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) == 0 {
		return nil
	}

	return p.pickAvailableLocked(available, exclude, true)
}

func (p *aggregatePool) HasAvailable(available func(*aggregateChildEntry) bool, exclude map[string]struct{}) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pickAvailableLocked(available, exclude, false) != nil
}

func (p *aggregatePool) pickAvailableLocked(available func(*aggregateChildEntry) bool, exclude map[string]struct{}, useStrategy bool) *aggregateChildEntry {
	isExcluded := func(e *aggregateChildEntry) bool {
		if len(exclude) == 0 {
			return false
		}
		_, ok := exclude[e.name]
		return ok
	}
	if available == nil {
		available = func(*aggregateChildEntry) bool { return true }
	}

	candidates := make([]int, 0, len(p.entries))
	minPrioritySet := false
	minPriority := 0
	for i := range p.entries {
		e := &p.entries[i]
		if isExcluded(e) || !available(e) {
			continue
		}
		if !minPrioritySet {
			minPriority = e.priority
			minPrioritySet = true
		}
		if e.priority > minPriority {
			break
		}
		candidates = append(candidates, i)
	}
	if len(candidates) == 0 {
		return nil
	}
	if !useStrategy {
		return &p.entries[candidates[0]]
	}

	var picked *aggregateChildEntry
	switch p.strategy {
	case "random":
		picked, _ = p.pickRandomLocked(candidates)
	case "least_used":
		picked, _ = p.pickLeastLocked(candidates, false)
	case "least_requests":
		picked, _ = p.pickLeastLocked(candidates, true)
	default:
		picked, _ = p.pickRoundRobinLocked(candidates)
	}
	if picked != nil {
		return picked
	}
	return &p.entries[candidates[0]]
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
			aggRetry:    vendor.Aggregate.Retry,
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

func (r *Router) RecoverUpstreamKey(vendor, key string) bool {
	if r == nil {
		return false
	}
	v := r.vendors[strings.TrimSpace(vendor)]
	if v == nil || v.pool == nil {
		return false
	}
	return v.pool.RecoverKey(key)
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
