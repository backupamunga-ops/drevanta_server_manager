// Per-account failure throttling: an attacker with many source addresses
// can spread guesses past the per-IP rate limiter; this caps repeated
// FAILED attempts against one account (identifier+target pair) no matter
// where they come from.
//
// In-memory only: a daemon restart clears it (the per-IP limiter and the
// audit log remain). A successful sign-in resets the account's counter.
package auth

import (
	"sync"
	"time"
)

// FailureLimiter records failed attempts per key and locks the key out
// after `max` failures inside `window` for `lockout`.
type FailureLimiter struct {
	mu      sync.Mutex
	fails   map[string]*failureRec
	max     int
	window  time.Duration
	lockout time.Duration
	now     func() time.Time
}

type failureRec struct {
	count       int
	firstAt     time.Time
	lockedUntil time.Time
}

func NewFailureLimiter(max int, window, lockout time.Duration) *FailureLimiter {
	return &FailureLimiter{
		fails:   make(map[string]*failureRec),
		max:     max,
		window:  window,
		lockout: lockout,
		now:     time.Now,
	}
}

// Locked reports whether the key is currently locked out.
func (f *FailureLimiter) Locked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.fails[key]
	if !ok {
		return false
	}
	now := f.now()
	if now.Before(rec.lockedUntil) {
		return true
	}
	if now.Sub(rec.firstAt) > f.window {
		delete(f.fails, key) // window expired, clean slate
	}
	return false
}

// Record adds one failure and reports whether the key just became (or
// already was) locked out.
func (f *FailureLimiter) Record(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	rec, ok := f.fails[key]
	if !ok || now.Sub(rec.firstAt) > f.window {
		rec = &failureRec{firstAt: now}
		f.fails[key] = rec
	}
	rec.count++
	if rec.count >= f.max {
		rec.lockedUntil = now.Add(f.lockout)
		return true
	}
	return false
}

// Reset clears the key after a successful sign-in.
func (f *FailureLimiter) Reset(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.fails, key)
}
