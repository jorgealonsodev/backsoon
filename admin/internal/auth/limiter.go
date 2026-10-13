package auth

import (
	"sync"
	"time"
)

// maxTracked bounds the number of client keys the limiter remembers.
const maxTracked = 4096

// Login rate-limit parameters (RNF-8).
const (
	LoginMaxAttempts = 5
	LoginWindow      = 15 * time.Minute
)

// Limiter limits login attempts per client key (normally the client IP) with a sliding window:
// at most max attempts per window.
//
// Attempt records the attempt up front, so concurrent requests cannot exceed the limit; it counts
// as a failure until Success is called. A rejected attempt is not recorded and does not extend the
// block: the key is admitted again once the oldest recorded attempt leaves the window.
//
// Success clears the key's history. This can never admit a sixth attempt inside the window, because
// Success is only reachable after an Attempt that was admitted, which requires fewer than max
// recorded attempts.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	attempts  map[string][]time.Time
	lastPrune time.Time
}

// NewLimiter returns a Limiter allowing max attempts per window. A nil clock means time.Now.
func NewLimiter(max int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{max: max, window: window, now: now, attempts: make(map[string][]time.Time)}
}

// Attempt records a login attempt for key. It returns false with the time to wait when the key
// already used its attempts inside the window; in that case the credentials must not be checked.
func (l *Limiter) Attempt(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.maybePrune(now)

	recent := l.recent(key, now)
	if len(recent) >= l.max {
		l.attempts[key] = recent
		return false, recent[0].Add(l.window).Sub(now)
	}
	if _, known := l.attempts[key]; !known && len(l.attempts) >= maxTracked {
		l.evictOldest()
	}
	l.attempts[key] = append(recent, now)
	return true, 0
}

// Success forgets the attempts of key after a correct login.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// recent returns the attempts of key still inside the window.
func (l *Limiter) recent(key string, now time.Time) []time.Time {
	times := l.attempts[key]
	i := 0
	for i < len(times) && !now.Before(times[i].Add(l.window)) {
		i++
	}
	return times[i:]
}

// maybePrune drops expired keys at most once a minute, keeping memory proportional to active keys.
func (l *Limiter) maybePrune(now time.Time) {
	if now.Sub(l.lastPrune) < time.Minute {
		return
	}
	l.lastPrune = now
	for key := range l.attempts {
		if recent := l.recent(key, now); len(recent) == 0 {
			delete(l.attempts, key)
		} else {
			l.attempts[key] = recent
		}
	}
}

// evictOldest removes the key whose latest attempt is the oldest. Only used when the table is full
// of live keys, which keeps memory bounded under a flood of distinct clients.
func (l *Limiter) evictOldest() {
	var victim string
	var oldest time.Time
	first := true
	for key, times := range l.attempts {
		last := times[len(times)-1]
		if first || last.Before(oldest) {
			victim, oldest, first = key, last, false
		}
	}
	delete(l.attempts, victim)
}

func (l *Limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.attempts)
}
