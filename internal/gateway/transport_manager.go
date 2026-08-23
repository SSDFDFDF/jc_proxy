package gateway

import (
	"net"
	"net/http"
	"sync"
	"time"

	"jc_proxy/internal/config"
)

// transportManager owns the long-lived connection pools used by immutable
// Router snapshots. Routers and vendor clients may be rebuilt frequently when
// keys or configuration change, but transports should survive those rebuilds.
type transportManager struct {
	mu         sync.Mutex
	transports map[transportConfigKey]*http.Transport
}

type transportConfigKey struct {
	responseHeaderTimeout time.Duration
}

func newTransportManager() *transportManager {
	return &transportManager{transports: make(map[transportConfigKey]*http.Transport)}
}

func (m *transportManager) Transport(upstream config.UpstreamConfig) *http.Transport {
	if m == nil {
		return newUpstreamTransport(upstream)
	}
	key := transportConfigKey{responseHeaderTimeout: upstreamResponseHeaderTimeout(upstream)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if transport := m.transports[key]; transport != nil {
		return transport
	}
	transport := newUpstreamTransport(upstream)
	m.transports[key] = transport
	return transport
}

func (m *transportManager) CloseIdleConnections() {
	if m == nil {
		return
	}
	m.mu.Lock()
	transports := make([]*http.Transport, 0, len(m.transports))
	for _, transport := range m.transports {
		transports = append(transports, transport)
	}
	m.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

func (m *transportManager) Retain(active map[*http.Transport]struct{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	stale := make([]*http.Transport, 0)
	for key, transport := range m.transports {
		if _, ok := active[transport]; ok {
			continue
		}
		delete(m.transports, key)
		stale = append(stale, transport)
	}
	m.mu.Unlock()
	for _, transport := range stale {
		// In-flight requests keep their transport pointer and active connections.
		// Only idle connections belonging to obsolete settings are closed.
		transport.CloseIdleConnections()
	}
}

func newUpstreamTransport(upstream config.UpstreamConfig) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          512,
		MaxIdleConnsPerHost:   128,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: upstreamResponseHeaderTimeout(upstream),
		ReadBufferSize:        16 << 10,
		WriteBufferSize:       16 << 10,
	}
}
