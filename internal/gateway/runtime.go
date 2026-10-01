package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

type UpstreamKeySource interface {
	ListAll() (map[string][]keystore.Record, error)
}

type UpstreamKeyController interface {
	UpstreamKeySource
	SetStatus(vendor, key, status, reason, actor string) error
}

type Runtime struct {
	updateMu   sync.Mutex
	router     atomic.Pointer[Router]
	cfg        atomic.Pointer[config.Config]
	keySource  UpstreamKeySource
	keyCtrl    UpstreamKeyController
	stats      *runtimeStatsRegistry
	transports *transportManager
}

func NewRuntime(cfg *config.Config, keySource UpstreamKeySource) (*Runtime, error) {
	cloned, err := cfg.Clone()
	if err != nil {
		return nil, err
	}
	rt := &Runtime{keySource: keySource, stats: newRuntimeStatsRegistry(), transports: newTransportManager()}
	if ctrl, ok := keySource.(UpstreamKeyController); ok {
		rt.keyCtrl = ctrl
	}
	rt.cfg.Store(cloned)
	r, err := rt.buildRouter(cloned)
	if err != nil {
		return nil, err
	}
	rt.router.Store(r)
	rt.transports.Retain(r.upstreamTransports())
	return rt, nil
}

func (rt *Runtime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rt.router.Load().ServeHTTP(w, req)
}

func (rt *Runtime) Update(cfg *config.Config) error {
	return rt.UpdateAndPersist(cfg, nil)
}

// UpdateAndPersist prepares a complete router before persistence. No live key
// health is changed until persistence succeeds; publishing then cannot fail.
// The callback must not re-enter Runtime mutation methods.
func (rt *Runtime) UpdateAndPersist(cfg *config.Config, persist func(*config.Config) error) error {
	rt.updateMu.Lock()
	defer rt.updateMu.Unlock()
	return rt.updateLocked(cfg, persist)
}

func (rt *Runtime) updateLocked(cfg *config.Config, persist func(*config.Config) error) error {
	if cfg == nil {
		return errors.New("runtime config is nil")
	}
	cloned, err := cfg.Clone()
	if err != nil {
		return err
	}
	prev := rt.router.Load()
	r, err := rt.buildRouter(cloned)
	if err != nil {
		rt.transports.Retain(prev.upstreamTransports())
		return fmt.Errorf("rebuild router: %w", err)
	}
	if persist != nil {
		if err := persist(cloned); err != nil {
			rt.transports.Retain(prev.upstreamTransports())
			return err
		}
	}
	r.ShareRuntimeStateFrom(prev)
	rt.router.Store(r)
	rt.cfg.Store(cloned)
	rt.transports.Retain(r.upstreamTransports())
	return nil
}

func (rt *Runtime) RefreshKeys() error {
	// Read the current config under the same lock as publication. Otherwise a
	// waiting RefreshKeys could restore a config superseded by an admin edit.
	rt.updateMu.Lock()
	defer rt.updateMu.Unlock()
	current := rt.cfg.Load()
	if current == nil {
		return errors.New("runtime config is empty")
	}
	return rt.updateLocked(current, nil)
}

func (rt *Runtime) Snapshot() *Router {
	return rt.router.Load()
}

func (rt *Runtime) Close() error {
	if rt != nil {
		rt.transports.CloseIdleConnections()
	}
	return nil
}

func (rt *Runtime) RecoverUpstreamKey(vendorID, key string) bool {
	r := rt.router.Load()
	if r == nil {
		return false
	}
	return r.RecoverUpstreamKey(vendorID, key)
}

func (rt *Runtime) buildRouter(cfg *config.Config) (*Router, error) {
	if rt.keySource == nil {
		return newRouterWithUpstreamKeyRecords(cfg, nil, rt.keyCtrl, rt.stats, rt.transports)
	}
	keys, err := rt.keySource.ListAll()
	if err != nil {
		return nil, fmt.Errorf("load upstream keys: %w", err)
	}
	return newRouterWithUpstreamKeyRecords(cfg, keys, rt.keyCtrl, rt.stats, rt.transports)
}
