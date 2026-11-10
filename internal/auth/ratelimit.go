package auth

import (
	"sync"
	"time"
)

// RateLimiter is a minimal fixed-window limiter for the small set of
// unauthenticated endpoints (login, claim, MFA verify). The daemon is
// loopback-only, so this is defense-in-depth against a runaway script or
// a forwarded request flood, not an internet-scale control.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

type rateBucket struct {
	count int
	start time.Time
}

// NewRateLimiter allows limit events per window per key.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*rateBucket),
		limit:   limit,
		window:  window,
		now:     time.Now,
	}
}

// Allow reports whether one event for key fits the current window.
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	b, ok := r.buckets[key]
	if !ok || now.Sub(b.start) >= r.window {
		// Opportunistic prune keeps the map bounded on a long-lived daemon.
		if len(r.buckets) > 1024 {
			for k, v := range r.buckets {
				if now.Sub(v.start) >= r.window {
					delete(r.buckets, k)
				}
			}
		}
		r.buckets[key] = &rateBucket{count: 1, start: now}
		return true
	}
	if b.count >= r.limit {
		return false
	}
	b.count++
	return true
}
