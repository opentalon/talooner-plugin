package service

import (
	"sync"
	"time"
)

// defaultRateLimit is the per-API-key request ceiling per minute when the
// config does not set one.
const defaultRateLimit = 60

// rateLimiter is a per-API-key token bucket. The endpoint is on the internet
// and the API key is the whole gate; a leaked key's first symptom is spend, so
// capping request rate per key caps the noise (P-D3, deployment.md).
type rateLimiter struct {
	mu      sync.Mutex
	perMin  float64
	buckets map[string]*tokenBucket
	now     func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	if perMinute <= 0 {
		perMinute = defaultRateLimit
	}
	return &rateLimiter{
		perMin:  float64(perMinute),
		buckets: map[string]*tokenBucket{},
		now:     time.Now,
	}
}

// Allow reports whether a request under key may proceed, consuming one token.
// A bucket starts full (burst = the per-minute limit) and refills continuously.
func (l *rateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: l.perMin, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.perMin, b.tokens+now.Sub(b.last).Minutes()*l.perMin)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
