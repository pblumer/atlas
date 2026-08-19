package api

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket guarding the unauthenticated public
// endpoints (ADR-0029): a public write is abuse-exposed, so a burst from one
// client is throttled. It is deliberately simple — a real internet deployment
// fronts the server with a reverse proxy (like /mcp, ADR-0016) — but it means a
// naive script can't hammer a public start link out of the box.
//
// Each key (the client IP) gets `capacity` tokens that refill at `refill` per
// second; a request costs one token. now is injectable so tests are deterministic.
type rateLimiter struct {
	mu       sync.Mutex
	tokens   map[string]float64
	last     map[string]time.Time
	capacity float64
	refill   float64
	now      func() time.Time
}

func newRateLimiter(capacity, refillPerSec float64) *rateLimiter {
	return &rateLimiter{
		tokens:   map[string]float64{},
		last:     map[string]time.Time{},
		capacity: capacity,
		refill:   refillPerSec,
		now:      time.Now,
	}
}

// allow reports whether a request keyed by key may proceed, consuming a token if
// so. Unknown keys start full.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	t, seen := l.tokens[key]
	if !seen {
		t = l.capacity
	} else if last, ok := l.last[key]; ok {
		t += now.Sub(last).Seconds() * l.refill
		if t > l.capacity {
			t = l.capacity
		}
	}
	// Bound the map so a flood of distinct keys can't grow it without limit; when
	// it gets large, drop the bookkeeping (every key then restarts full, which is
	// safe — it only ever grants tokens, never wrongly denies a real client).
	if len(l.tokens) > 10000 {
		l.tokens = map[string]float64{}
		l.last = map[string]time.Time{}
		t = l.capacity
	}
	l.last[key] = now
	if t < 1 {
		l.tokens[key] = t
		return false
	}
	l.tokens[key] = t - 1
	return true
}
