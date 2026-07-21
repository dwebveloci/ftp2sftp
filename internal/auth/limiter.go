package auth

import (
	"sync"
	"sync/atomic"
	"time"
)

// Limiter implements a simple sliding-window failure counter with
// progressive lockout, keyed by an arbitrary string (client IP, username,
// or a composite of both). It is safe for concurrent use.
//
// State is kept in memory only, bounded by periodic sweeps of stale
// entries; there is no background goroutine, so its lifecycle is tied
// entirely to the caller (no explicit Close needed).
type Limiter struct {
	mu    sync.Mutex
	state map[string]*attemptState

	maxFailures int
	window      time.Duration
	lockout     time.Duration

	calls atomic.Uint64
}

type attemptState struct {
	failures     int
	windowStart  time.Time
	lockedUntil  time.Time
	lastActivity time.Time
}

// NewLimiter creates a Limiter that locks a key out for lockout once it
// accumulates maxFailures failures within window.
func NewLimiter(maxFailures int, window, lockout time.Duration) *Limiter {
	return &Limiter{
		state:       make(map[string]*attemptState),
		maxFailures: maxFailures,
		window:      window,
		lockout:     lockout,
	}
}

// Allow reports whether an attempt for key is currently permitted.
func (l *Limiter) Allow(key string) bool {
	l.sweepOccasionally()

	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.state[key]
	if !ok {
		return true
	}

	now := time.Now()
	if !s.lockedUntil.IsZero() && now.Before(s.lockedUntil) {
		return false
	}

	return true
}

// RecordFailure registers a failed attempt for key, applying progressive
// lockout once maxFailures is reached within the configured window.
func (l *Limiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	s, ok := l.state[key]
	if !ok || now.Sub(s.windowStart) > l.window {
		s = &attemptState{windowStart: now}
		l.state[key] = s
	}

	s.failures++
	s.lastActivity = now

	if s.failures >= l.maxFailures {
		// Progressive lockout: each additional failure beyond the
		// threshold multiplies the lockout duration, capped at 32x.
		multiplier := s.failures - l.maxFailures + 1
		if multiplier > 32 {
			multiplier = 32
		}

		s.lockedUntil = now.Add(l.lockout * time.Duration(multiplier))
	}
}

// RecordSuccess clears any failure state for key.
func (l *Limiter) RecordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.state, key)
}

// sweepOccasionally removes stale entries every 256 calls so the map does
// not grow unbounded under a distributed guessing attempt using many
// distinct source keys.
func (l *Limiter) sweepOccasionally() {
	if l.calls.Add(1)%256 != 0 {
		return
	}

	cutoff := time.Now().Add(-(l.window + l.lockout*32))

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, s := range l.state {
		if s.lastActivity.Before(cutoff) && time.Now().After(s.lockedUntil) {
			delete(l.state, key)
		}
	}
}
