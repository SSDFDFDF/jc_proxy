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
	updateMu  sync.Mutex
	router    atomic.Pointer[Router]
	cfg       atomic.Pointer[config.Config]
	keySource UpstreamKeySource
	keyCtrl   UpstreamKeyController
	stats     *runtimeStatsRegistry
}

func NewRuntime(cfg *config.Config, keySource UpstreamKeySource) (*Runtime, error) {
	cloned, err := cfg.Clone()
	if err != nil {
		return nil, err
	}
	rt := &Runtime{keySource: keySource, stats: newRuntimeStatsRegistry()}
	if ctrl, ok := keySource.(UpstreamKeyController); ok {
		rt.keyCtrl = ctrl
	}
	rt.cfg.Store(cloned)
	r, err := rt.buildRouter(cloned)
	if err != nil {
		return nil, err
	}
	rt.router.Store(r)
	return rt, nil
}

func (rt *Runtime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rt.router.Load().ServeHTTP(w, req)
}

func (rt *Runtime) Update(cfg *config.Config) error {
	cloned, err := cfg.Clone()
	if err != nil {
		return err
	}
	rt.updateMu.Lock()
	defer rt.updateMu.Unlock()
	r, err := rt.buildRouter(cloned)
	if err != nil {
		return fmt.Errorf("rebuild router: %w", err)
	}
	rt.router.Store(r)
	rt.cfg.Store(cloned)
	return nil
}

func (rt *Runtime) RefreshKeys() error {
	current := rt.cfg.Load()
	if current == nil {
		return errors.New("runtime config is empty")
	}
	return rt.Update(current)
}

func (rt *Runtime) Snapshot() *Router {
	return rt.router.Load()
}

func (rt *Runtime) buildRouter(cfg *config.Config) (*Router, error) {
	if rt.keySource == nil {
		return newRouterWithUpstreamKeyRecords(cfg, nil, rt.keyCtrl, rt.stats)
	}
	keys, err := rt.keySource.ListAll()
	if err != nil {
		return nil, fmt.Errorf("load upstream keys: %w", err)
	}
	return newRouterWithUpstreamKeyRecords(cfg, keys, rt.keyCtrl, rt.stats)
}
