package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

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
	mu        sync.RWMutex
	router    *Router
	cfg       *config.Config
	keySource UpstreamKeySource
	keyCtrl   UpstreamKeyController
}

func NewRuntime(cfg *config.Config, keySource UpstreamKeySource) (*Runtime, error) {
	cloned, err := cfg.Clone()
	if err != nil {
		return nil, err
	}
	rt := &Runtime{cfg: cloned, keySource: keySource}
	if ctrl, ok := keySource.(UpstreamKeyController); ok {
		rt.keyCtrl = ctrl
	}
	r, err := rt.buildRouter(cloned)
	if err != nil {
		return nil, err
	}
	rt.router = r
	return rt, nil
}

func (rt *Runtime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rt.mu.RLock()
	r := rt.router
	rt.mu.RUnlock()
	r.ServeHTTP(w, req)
}

func (rt *Runtime) Update(cfg *config.Config) error {
	cloned, err := cfg.Clone()
	if err != nil {
		return err
	}
	r, err := rt.buildRouter(cloned)
	if err != nil {
		return fmt.Errorf("rebuild router: %w", err)
	}
	rt.mu.Lock()
	rt.router = r
	rt.cfg = cloned
	rt.mu.Unlock()
	return nil
}

func (rt *Runtime) RefreshKeys() error {
	rt.mu.RLock()
	current := rt.cfg
	rt.mu.RUnlock()
	if current == nil {
		return errors.New("runtime config is empty")
	}
	return rt.Update(current)
}

func (rt *Runtime) Snapshot() *Router {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.router
}

func (rt *Runtime) buildRouter(cfg *config.Config) (*Router, error) {
	if rt.keySource == nil {
		return New(cfg)
	}
	keys, err := rt.keySource.ListAll()
	if err != nil {
		return nil, fmt.Errorf("load upstream keys: %w", err)
	}
	return NewWithUpstreamKeyRecords(cfg, keys, rt.keyCtrl)
}
