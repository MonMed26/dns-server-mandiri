package cache

import (
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Entry represents a cached DNS response
type Entry struct {
	Msg       *dns.Msg
	CreatedAt time.Time
	ExpiresAt time.Time
	TTL       time.Duration
	HitCount  uint64
	Negative  bool // NXDOMAIN or NODATA
}

// IsExpired checks if the cache entry has expired
func (e *Entry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// RemainingTTL returns the remaining TTL for this entry
func (e *Entry) RemainingTTL() time.Duration {
	remaining := time.Until(e.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// ShouldPrefetch returns true if the entry should be prefetched
func (e *Entry) ShouldPrefetch(ratio float64) bool {
	elapsed := time.Since(e.CreatedAt)
	return elapsed > time.Duration(float64(e.TTL)*(1-ratio))
}

// Cache is a thread-safe DNS cache with TTL management
type Cache struct {
	mu              sync.RWMutex
	entries         map[string]*Entry
	maxSize         int
	minTTL          time.Duration
	maxTTL          time.Duration
	negativeTTL     time.Duration
	prefetchRatio   float64
	cleanupInterval time.Duration
	stopCleanup     chan struct{}

	// Stats
	hits   uint64
	misses uint64
	evictions uint64
}

// New creates a new DNS cache
func New(maxSize int, minTTL, maxTTL, negativeTTL time.Duration, prefetchRatio float64, cleanupInterval time.Duration) *Cache {
	c := &Cache{
		entries:         make(map[string]*Entry),
		maxSize:         maxSize,
		minTTL:          minTTL,
		maxTTL:          maxTTL,
		negativeTTL:     negativeTTL,
		prefetchRatio:   prefetchRatio,
		cleanupInterval: cleanupInterval,
		stopCleanup:     make(chan struct{}),
	}

	go c.cleanupLoop()
	return c
}

// cacheKey generates a unique key for a DNS question
func cacheKey(name string, qtype uint16, qclass uint16) string {
	return dns.CanonicalName(name) + "/" + dns.TypeToString[qtype] + "/" + dns.ClassToString[qclass]
}

// Get retrieves a cached DNS response
func (c *Cache) Get(name string, qtype uint16, qclass uint16) (*dns.Msg, bool, bool) {
	key := cacheKey(name, qtype, qclass)

	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	if !exists {
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		return nil, false, false
	}

	if entry.IsExpired() {
		c.mu.Lock()
		delete(c.entries, key)
		c.misses++
		c.mu.Unlock()
		return nil, false, false
	}

	c.mu.Lock()
	entry.HitCount++
	c.hits++
	c.mu.Unlock()

	// Clone the message and adjust TTLs
	msg := entry.Msg.Copy()
	remaining := uint32(entry.RemainingTTL().Seconds())
	if remaining == 0 {
		remaining = 1
	}

	// Adjust TTLs in all sections
	for _, rr := range msg.Answer {
		rr.Header().Ttl = remaining
	}
	for _, rr := range msg.Ns {
		rr.Header().Ttl = remaining
	}
	for _, rr := range msg.Extra {
		if rr.Header().Rrtype != dns.TypeOPT {
			rr.Header().Ttl = remaining
		}
	}

	shouldPrefetch := entry.ShouldPrefetch(c.prefetchRatio)
	return msg, true, shouldPrefetch
}

// Set stores a DNS response in the cache
func (c *Cache) Set(name string, qtype uint16, qclass uint16, msg *dns.Msg) {
	if msg == nil {
		return
	}

	// Determine TTL from the response
	ttl := c.extractTTL(msg)

	// Enforce min/max TTL
	if ttl < c.minTTL {
		ttl = c.minTTL
	}
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}

	// Check if this is a negative response
	negative := msg.Rcode == dns.RcodeNameError || (msg.Rcode == dns.RcodeSuccess && len(msg.Answer) == 0)
	if negative {
		ttl = c.negativeTTL
	}

	key := cacheKey(name, qtype, qclass)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[key] = &Entry{
		Msg:       msg.Copy(),
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
		TTL:       ttl,
		HitCount:  0,
		Negative:  negative,
	}
}

// extractTTL gets the minimum TTL from a DNS message
func (c *Cache) extractTTL(msg *dns.Msg) time.Duration {
	var minTTL uint32 = 3600 // default 1 hour

	for _, rr := range msg.Answer {
		if rr.Header().Ttl < minTTL {
			minTTL = rr.Header().Ttl
		}
	}
	for _, rr := range msg.Ns {
		if rr.Header().Ttl < minTTL {
			minTTL = rr.Header().Ttl
		}
	}

	if minTTL == 0 {
		minTTL = 30
	}

	return time.Duration(minTTL) * time.Second
}

// evictOldest removes the oldest/least-used entries
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, entry := range c.entries {
		if entry.IsExpired() {
			delete(c.entries, key)
			c.evictions++
			continue
		}
		if first || entry.CreatedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.CreatedAt
			first = false
		}
	}

	if oldestKey != "" && len(c.entries) >= c.maxSize {
		delete(c.entries, oldestKey)
		c.evictions++
	}
}

// cleanupLoop periodically removes expired entries
func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCleanup:
			return
		}
	}
}

// cleanup removes all expired entries
func (c *Cache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if entry.IsExpired() {
			delete(c.entries, key)
			c.evictions++
		}
	}
}

// Stats returns cache statistics
func (c *Cache) Stats() (size int, hits, misses, evictions uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries), c.hits, c.misses, c.evictions
}

// Stop stops the cache cleanup goroutine
func (c *Cache) Stop() {
	close(c.stopCleanup)
}

// PrefetchCandidates returns entries that should be prefetched
func (c *Cache) PrefetchCandidates() []struct {
	Name   string
	Qtype  uint16
	Qclass uint16
} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var candidates []struct {
		Name   string
		Qtype  uint16
		Qclass uint16
	}

	for _, entry := range c.entries {
		if entry.ShouldPrefetch(c.prefetchRatio) && !entry.IsExpired() && entry.HitCount > 2 {
			if len(entry.Msg.Question) > 0 {
				q := entry.Msg.Question[0]
				candidates = append(candidates, struct {
					Name   string
					Qtype  uint16
					Qclass uint16
				}{q.Name, q.Qtype, q.Qclass})
			}
		}
	}

	return candidates
}
