package ratelimit

import (
	"sync"
	"time"
)

// Limiter implements a per-IP token bucket rate limiter
type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int           // tokens per second
	burst    int           // max burst size
	cleanup  time.Duration // cleanup interval
	stop     chan struct{}
}

type bucket struct {
	tokens    float64
	lastTime  time.Time
	blocked   int // number of times blocked
}

// New creates a new rate limiter
func New(requestsPerSec int, burstSize int, cleanupInterval time.Duration) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		rate:    requestsPerSec,
		burst:   burstSize,
		cleanup: cleanupInterval,
		stop:    make(chan struct{}),
	}

	go l.cleanupLoop()
	return l
}

// Allow checks if a request from the given IP should be allowed
func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, exists := l.buckets[ip]
	if !exists {
		b = &bucket{
			tokens:   float64(l.burst),
			lastTime: now,
		}
		l.buckets[ip] = b
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * float64(l.rate)
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
	b.lastTime = now

	// Check if we have tokens available
	if b.tokens >= 1 {
		b.tokens--
		return true
	}

	b.blocked++
	return false
}

// GetStats returns rate limit statistics for an IP
func (l *Limiter) GetStats(ip string) (blocked int, tokens float64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if b, exists := l.buckets[ip]; exists {
		return b.blocked, b.tokens
	}
	return 0, 0
}

// cleanupLoop removes stale buckets periodically
func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(l.cleanup)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.cleanupStale()
		case <-l.stop:
			return
		}
	}
}

// cleanupStale removes buckets that haven't been used recently
func (l *Limiter) cleanupStale() {
	l.mu.Lock()
	defer l.mu.Unlock()

	threshold := time.Now().Add(-l.cleanup * 2)
	for ip, b := range l.buckets {
		if b.lastTime.Before(threshold) {
			delete(l.buckets, ip)
		}
	}
}

// Stop stops the rate limiter cleanup goroutine
func (l *Limiter) Stop() {
	close(l.stop)
}

// ActiveClients returns the number of active client IPs
func (l *Limiter) ActiveClients() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ClientRateStat represents rate limit stats for a single client
type ClientRateStat struct {
	IP      string  `json:"ip"`
	Blocked int     `json:"blocked_count"`
	Tokens  float64 `json:"tokens_remaining"`
}

// GetAllStats returns rate limit statistics for all active clients
func (l *Limiter) GetAllStats() []ClientRateStat {
	l.mu.Lock()
	defer l.mu.Unlock()

	stats := make([]ClientRateStat, 0, len(l.buckets))
	for ip, b := range l.buckets {
		if b.blocked > 0 {
			stats = append(stats, ClientRateStat{
				IP:      ip,
				Blocked: b.blocked,
				Tokens:  b.tokens,
			})
		}
	}
	return stats
}
