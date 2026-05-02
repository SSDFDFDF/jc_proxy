package balancer

import (
	"fmt"
	"testing"

	"jc_proxy/internal/keystore"
)

func benchPoolKeys(n int) []KeyConfig {
	out := make([]KeyConfig, 0, n)
	for i := range n {
		out = append(out, KeyConfig{
			Key:    fmt.Sprintf("key-%04d", i),
			Status: keystore.KeyStatusActive,
		})
	}
	return out
}

func benchAcquireRelease(b *testing.B, strategy string, keys int) {
	b.Helper()
	pool, err := NewPoolWithConfigs(strategy, benchPoolKeys(keys))
	if err != nil {
		b.Fatalf("init pool: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx, _, ok := pool.Acquire()
			if !ok {
				b.Fatal("acquire failed")
			}
			pool.ReleaseSuccess(idx)
		}
	})
}

func BenchmarkPool_Acquire_RoundRobin(b *testing.B) {
	benchAcquireRelease(b, "round_robin", 100)
}

func BenchmarkPool_Acquire_Random(b *testing.B) {
	benchAcquireRelease(b, "random", 100)
}

func BenchmarkPool_Acquire_LeastUsed(b *testing.B) {
	benchAcquireRelease(b, "least_used", 100)
}

func BenchmarkPool_Acquire_LeastRequests(b *testing.B) {
	benchAcquireRelease(b, "least_requests", 100)
}

func BenchmarkPool_Snapshot(b *testing.B) {
	pool, err := NewPoolWithConfigs("round_robin", benchPoolKeys(100))
	if err != nil {
		b.Fatalf("init pool: %v", err)
	}
	for range 50 {
		idx, _, _ := pool.Acquire()
		pool.ReleaseSuccess(idx)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = pool.Snapshot()
	}
}
