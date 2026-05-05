package clientstats

import (
	"sort"
	"sync"
	"time"
)

// ClientInfo tracks per-client DNS statistics
type ClientInfo struct {
	IP            string             `json:"ip"`
	TotalQueries  uint64             `json:"total_queries"`
	BlockedCount  uint64             `json:"blocked_count"`
	FirstSeen     time.Time          `json:"first_seen"`
	LastSeen      time.Time          `json:"last_seen"`
	TopDomains    map[string]uint64  `json:"-"` // internal tracking
	AvgLatencyNs  int64              `json:"-"`
	QueryCount    int64              `json:"-"`
}

// ClientSummary is the JSON-friendly summary
type ClientSummary struct {
	IP           string        `json:"ip"`
	TotalQueries uint64        `json:"total_queries"`
	BlockedCount uint64        `json:"blocked_count"`
	FirstSeen    time.Time     `json:"first_seen"`
	LastSeen     time.Time     `json:"last_seen"`
	TopDomains   []DomainCount `json:"top_domains"`
	AvgLatencyMs float64       `json:"avg_latency_ms"`
}

// DomainCount represents a domain with its query count
type DomainCount struct {
	Domain string `json:"domain"`
	Count  uint64 `json:"count"`
}

// Tracker tracks per-client statistics
type Tracker struct {
	mu      sync.RWMutex
	clients map[string]*ClientInfo
	maxClients int
}

// New creates a new client stats tracker
func New(maxClients int) *Tracker {
	if maxClients <= 0 {
		maxClients = 10000
	}
	return &Tracker{
		clients:    make(map[string]*ClientInfo),
		maxClients: maxClients,
	}
}

// RecordQuery records a DNS query for a client
func (t *Tracker) RecordQuery(clientIP string, domain string, blocked bool, latency time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	client, exists := t.clients[clientIP]
	if !exists {
		if len(t.clients) >= t.maxClients {
			// Evict oldest client
			t.evictOldest()
		}
		client = &ClientInfo{
			IP:         clientIP,
			FirstSeen:  time.Now(),
			TopDomains: make(map[string]uint64),
		}
		t.clients[clientIP] = client
	}

	client.TotalQueries++
	client.LastSeen = time.Now()
	client.AvgLatencyNs += int64(latency)
	client.QueryCount++

	if blocked {
		client.BlockedCount++
	}

	// Track top domains (limit to 100 per client)
	if len(client.TopDomains) < 100 {
		client.TopDomains[domain]++
	} else if _, ok := client.TopDomains[domain]; ok {
		client.TopDomains[domain]++
	}
}

// GetClient returns stats for a specific client
func (t *Tracker) GetClient(ip string) *ClientSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	client, exists := t.clients[ip]
	if !exists {
		return nil
	}

	return t.buildSummary(client)
}

// GetTopClients returns the top N clients by query count
func (t *Tracker) GetTopClients(limit int) []ClientSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Collect all clients
	clients := make([]*ClientInfo, 0, len(t.clients))
	for _, c := range t.clients {
		clients = append(clients, c)
	}

	// Sort by total queries
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].TotalQueries > clients[j].TotalQueries
	})

	if limit > len(clients) {
		limit = len(clients)
	}

	result := make([]ClientSummary, limit)
	for i := 0; i < limit; i++ {
		result[i] = *t.buildSummary(clients[i])
	}

	return result
}

// GetActiveClients returns number of clients active in the last duration
func (t *Tracker) GetActiveClients(since time.Duration) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	threshold := time.Now().Add(-since)
	count := 0
	for _, c := range t.clients {
		if c.LastSeen.After(threshold) {
			count++
		}
	}
	return count
}

// TotalClients returns total tracked clients
func (t *Tracker) TotalClients() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.clients)
}

func (t *Tracker) buildSummary(client *ClientInfo) *ClientSummary {
	// Get top 10 domains for this client
	type dc struct {
		domain string
		count  uint64
	}
	domains := make([]dc, 0, len(client.TopDomains))
	for d, c := range client.TopDomains {
		domains = append(domains, dc{d, c})
	}
	sort.Slice(domains, func(i, j int) bool {
		return domains[i].count > domains[j].count
	})

	topLimit := 10
	if topLimit > len(domains) {
		topLimit = len(domains)
	}

	topDomains := make([]DomainCount, topLimit)
	for i := 0; i < topLimit; i++ {
		topDomains[i] = DomainCount{Domain: domains[i].domain, Count: domains[i].count}
	}

	var avgLatency float64
	if client.QueryCount > 0 {
		avgLatency = float64(client.AvgLatencyNs) / float64(client.QueryCount) / 1e6
	}

	return &ClientSummary{
		IP:           client.IP,
		TotalQueries: client.TotalQueries,
		BlockedCount: client.BlockedCount,
		FirstSeen:    client.FirstSeen,
		LastSeen:     client.LastSeen,
		TopDomains:   topDomains,
		AvgLatencyMs: avgLatency,
	}
}

func (t *Tracker) evictOldest() {
	var oldestIP string
	var oldestTime time.Time
	first := true

	for ip, c := range t.clients {
		if first || c.LastSeen.Before(oldestTime) {
			oldestIP = ip
			oldestTime = c.LastSeen
			first = false
		}
	}

	if oldestIP != "" {
		delete(t.clients, oldestIP)
	}
}
