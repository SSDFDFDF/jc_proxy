package gateway

import (
	"net/http"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func TestTransportManagerRetainDropsObsoleteSettings(t *testing.T) {
	manager := newTransportManager()
	first := manager.Transport(config.UpstreamConfig{ResponseHeaderTimeout: durationPtrForTransportTest(5 * time.Second)})
	second := manager.Transport(config.UpstreamConfig{ResponseHeaderTimeout: durationPtrForTransportTest(15 * time.Second)})
	if first == second {
		t.Fatal("different settings unexpectedly shared transport")
	}

	manager.Retain(map[*http.Transport]struct{}{second: {}})
	recreated := manager.Transport(config.UpstreamConfig{ResponseHeaderTimeout: durationPtrForTransportTest(5 * time.Second)})
	if recreated == first {
		t.Fatal("obsolete transport was retained after Retain")
	}
	if manager.Transport(config.UpstreamConfig{ResponseHeaderTimeout: durationPtrForTransportTest(15 * time.Second)}) != second {
		t.Fatal("active transport was removed by Retain")
	}
}

func durationPtrForTransportTest(value time.Duration) *time.Duration {
	return &value
}
