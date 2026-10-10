package cf

import (
	"context"
	"sync"
	"time"
)

// rateLimiter enforces a fixed minimum interval between successive Wait
// releases. Callers wait FIFO on a channel; retries share the same budget
// as fresh requests.
//
// The Codeforces API caps at 0.5 req/sec globally. This limiter is the one
// place in the product that enforces it. Every service that talks to CF
// goes through a Client, which shares one limiter.
type rateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newRateLimiter(minInterval time.Duration) *rateLimiter {
	return &rateLimiter{interval: minInterval}
}

// Wait blocks until the caller is allowed to issue the next request, or
// ctx is cancelled. Callers are released strictly in the order they call
// Wait — this preserves fairness across goroutines.
func (l *rateLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	slot := l.next
	if slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	wait := time.Until(slot)
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}
