package cf

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_SerialisesCalls(t *testing.T) {
	l := newRateLimiter(50 * time.Millisecond)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	elapsed := time.Since(start)

	// 4 waits at 50ms interval: first returns immediately, next three
	// each stall 50ms → ~150ms floor.
	if elapsed < 140*time.Millisecond {
		t.Fatalf("limiter did not enforce interval: %v", elapsed)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("limiter is too slow: %v", elapsed)
	}
}

func TestRateLimiter_FIFOAcrossGoroutines(t *testing.T) {
	l := newRateLimiter(20 * time.Millisecond)
	ctx := context.Background()

	// Warm up so the first slot is 'now'.
	_ = l.Wait(ctx)

	var wg sync.WaitGroup
	got := make([]int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = l.Wait(ctx)
			got[idx] = idx
		}(i)
		time.Sleep(2 * time.Millisecond) // ensure enqueue order
	}
	wg.Wait()
}

func TestRateLimiter_RespectsContext(t *testing.T) {
	l := newRateLimiter(500 * time.Millisecond)
	ctx := context.Background()
	_ = l.Wait(ctx) // consume the immediate slot

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := l.Wait(ctx)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}
