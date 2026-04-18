package gateway

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"jc_proxy/internal/keystore"
)

const defaultRuntimeStatsFlushInterval = 5 * time.Second

type RuntimeStatsPersisterOptions struct {
	FlushInterval time.Duration
	ErrorHandler  func(error)
}

type RuntimeStatsPersister struct {
	runtime *Runtime
	store   keystore.RuntimeStatsStore

	interval time.Duration
	onError  func(error)

	mu   sync.Mutex
	last map[string]map[string]keystore.RuntimeStats
	stop chan struct{}
	done chan error
}

func NewRuntimeStatsPersister(runtime *Runtime, store keystore.RuntimeStatsStore, opts RuntimeStatsPersisterOptions) (*RuntimeStatsPersister, error) {
	if runtime == nil {
		return nil, errors.New("runtime is nil")
	}
	if store == nil {
		return nil, errors.New("runtime stats store is nil")
	}

	interval := opts.FlushInterval
	if interval <= 0 {
		interval = defaultRuntimeStatsFlushInterval
	}
	onError := opts.ErrorHandler
	if onError == nil {
		onError = func(err error) {
			log.Printf("persist runtime stats failed: %v", err)
		}
	}

	p := &RuntimeStatsPersister{
		runtime:  runtime,
		store:    store,
		interval: interval,
		onError:  onError,
		last:     captureRuntimeStats(runtime.Snapshot()),
		stop:     make(chan struct{}),
		done:     make(chan error, 1),
	}
	go p.run()
	return p, nil
}

func (p *RuntimeStatsPersister) Close() error {
	p.mu.Lock()
	select {
	case <-p.stop:
		p.mu.Unlock()
		return nil
	default:
		close(p.stop)
	}
	p.mu.Unlock()
	return <-p.done
}

func (p *RuntimeStatsPersister) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flushLocked()
}

func (p *RuntimeStatsPersister) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			err := p.flushLocked()
			p.mu.Unlock()
			if err != nil {
				p.onError(err)
			}
		case <-p.stop:
			p.mu.Lock()
			err := p.flushLocked()
			p.mu.Unlock()
			p.done <- err
			close(p.done)
			return
		}
	}
}

func (p *RuntimeStatsPersister) flushLocked() error {
	current := captureRuntimeStats(p.runtime.Snapshot())
	deltas, nextBaseline := buildRuntimeStatsDeltas(current, p.last)
	if len(deltas) == 0 {
		p.last = nextBaseline
		return nil
	}
	if err := p.store.ApplyRuntimeStatsDeltas(deltas); err != nil {
		return err
	}
	p.last = nextBaseline
	return nil
}

func captureRuntimeStats(router *Router) map[string]map[string]keystore.RuntimeStats {
	if router == nil {
		return map[string]map[string]keystore.RuntimeStats{}
	}

	out := make(map[string]map[string]keystore.RuntimeStats)
	for vendor, states := range router.VendorStateSnapshots() {
		if len(states) == 0 {
			continue
		}
		perVendor := make(map[string]keystore.RuntimeStats, len(states))
		for _, state := range states {
			key := strings.TrimSpace(state.Key)
			if key == "" {
				continue
			}
			perVendor[key] = state.RuntimeStats
		}
		if len(perVendor) > 0 {
			out[vendor] = perVendor
		}
	}
	return out
}

func buildRuntimeStatsDeltas(current, previous map[string]map[string]keystore.RuntimeStats) (map[string][]keystore.RuntimeStatsDelta, map[string]map[string]keystore.RuntimeStats) {
	nextBaseline := cloneRuntimeStatsSnapshot(current)
	for vendor, prevKeys := range previous {
		if len(prevKeys) == 0 {
			continue
		}
		nextKeys := nextBaseline[vendor]
		if nextKeys == nil {
			nextKeys = make(map[string]keystore.RuntimeStats, len(prevKeys))
			nextBaseline[vendor] = nextKeys
		}
		for key, prevStats := range prevKeys {
			if _, ok := nextKeys[key]; ok {
				continue
			}
			nextKeys[key] = prevStats
		}
	}
	deltas := make(map[string][]keystore.RuntimeStatsDelta)

	for vendor, currentKeys := range current {
		prevKeys := previous[vendor]
		for key, currentStats := range currentKeys {
			prevStats, ok := prevKeys[key]
			switch {
			case !ok:
				if currentStats.IsZero() {
					continue
				}
				deltas[vendor] = append(deltas[vendor], keystore.RuntimeStatsDelta{
					Key:          key,
					RuntimeStats: currentStats,
				})
			default:
				delta, monotonic := currentStats.DeltaSince(prevStats)
				if !monotonic {
					if currentStats.IsZero() {
						continue
					}
					deltas[vendor] = append(deltas[vendor], keystore.RuntimeStatsDelta{
						Key:          key,
						RuntimeStats: currentStats,
					})
					continue
				}
				if delta.IsZero() {
					continue
				}
				deltas[vendor] = append(deltas[vendor], keystore.RuntimeStatsDelta{
					Key:          key,
					RuntimeStats: delta,
				})
			}
		}
	}

	for vendor, records := range deltas {
		if len(records) == 0 {
			delete(deltas, vendor)
		}
	}
	return deltas, nextBaseline
}

func cloneRuntimeStatsSnapshot(src map[string]map[string]keystore.RuntimeStats) map[string]map[string]keystore.RuntimeStats {
	out := make(map[string]map[string]keystore.RuntimeStats, len(src))
	for vendor, records := range src {
		perVendor := make(map[string]keystore.RuntimeStats, len(records))
		for key, stats := range records {
			perVendor[key] = stats
		}
		out[vendor] = perVendor
	}
	return out
}
