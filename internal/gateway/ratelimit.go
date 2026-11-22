package gateway

import (
	"sync"
	"time"
)

// rateLimiter is a per-source-IP token bucket for new connections, a simple
// brute-force protection for publicly exposed SSH/RDP ports.
type rateLimiter struct {
	perMinute float64
	burst     float64

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &rateLimiter{perMinute: float64(perMinute), burst: float64(max(perMinute/2, 5)), buckets: map[string]*bucket{}}
}

// allow reports whether a new connection from ip is allowed now.
func (r *rateLimiter) allow(ip string, now time.Time) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.lastGC) > 5*time.Minute {
		for k, b := range r.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(r.buckets, k)
			}
		}
		r.lastGC = now
	}
	b, ok := r.buckets[ip]
	if !ok {
		b = &bucket{tokens: r.burst, last: now}
		r.buckets[ip] = b
	}
	b.tokens = min(r.burst, b.tokens+now.Sub(b.last).Minutes()*r.perMinute)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
