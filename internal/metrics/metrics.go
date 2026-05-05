package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// QueryLog represents a single logged DNS query
type QueryLog struct {
	Timestamp  time.Time `json:"timestamp"`
	ClientIP   string    `json:"client_ip"`
	Domain     string    `json:"domain"`
	QueryType  string    `json:"query_type"`
	Rcode      string    `json:"rcode"`
	LatencyMs  float64   `json:"latency_ms"`
	Cached     bool      `json:"cached"`
	Protocol   string    `json:"protocol"`
}

// DomainStat tracks per-domain statistics
type DomainStat struct {
	Domain     string  `json:"domain"`
	Count      uint64  `json:"count"`
	AvgLatency float64 `json:"avg_latency_ms"`
}

// QPSPoint represents a QPS data point for charting
type QPSPoint struct {
	Timestamp time.Time `json:"timestamp"`
	QPS       float64   `json:"qps"`
}

// Metrics tracks DNS server performance metrics
type Metrics struct {
	startTime time.Time

	// Query counters
	TotalQueries   atomic.Uint64
	UDPQueries     atomic.Uint64
	TCPQueries     atomic.Uint64
	SuccessQueries atomic.Uint64
	FailedQueries  atomic.Uint64
	NXDomain       atomic.Uint64
	ServFail       atomic.Uint64

	// Cache metrics
	CacheHits   atomic.Uint64
	CacheMisses atomic.Uint64

	// Rate limit
	RateLimited atomic.Uint64

	// Latency tracking
	TotalLatencyNs atomic.Int64
	QueryCount     atomic.Int64

	// Query log (ring buffer)
	queryLog    []QueryLog
	queryLogMu  sync.RWMutex
	queryLogMax int

	// Top domains tracking
	domainCounts   map[string]*domainCounter
	domainCountsMu sync.RWMutex

	// QPS history (for charts)
	qpsHistory   []QPSPoint
	qpsHistoryMu sync.RWMutex
	lastQPSCount uint64
	lastQPSTime  time.Time

	// Latency histogram (buckets in ms)
	latencyBuckets   [8]atomic.Uint64 // <1, <5, <10, <50, <100, <500, <1000, >=1000
	
	// Cache stats function
	CacheStatsFunc    func() (size int, hits, misses, evictions uint64)
	ActiveClientsFunc func() int

	stopChan chan struct{}
}

type domainCounter struct {
	count       uint64
	totalLatNs  int64
}

// New creates a new metrics instance
func New() *Metrics {
	m := &Metrics{
		startTime:    time.Now(),
		queryLogMax:  500, // Keep last 500 queries
		queryLog:     make([]QueryLog, 0, 500),
		domainCounts: make(map[string]*domainCounter),
		qpsHistory:   make([]QPSPoint, 0, 360), // 3 hours at 30s intervals
		lastQPSTime:  time.Now(),
		stopChan:     make(chan struct{}),
	}

	// Start QPS tracking goroutine
	go m.trackQPS()

	return m
}

// RecordQuery records a DNS query with full details
func (m *Metrics) RecordQuery(protocol string, rcode int, latency time.Duration) {
	m.TotalQueries.Add(1)

	switch protocol {
	case "udp":
		m.UDPQueries.Add(1)
	case "tcp":
		m.TCPQueries.Add(1)
	}

	switch rcode {
	case 0: // NOERROR
		m.SuccessQueries.Add(1)
	case 3: // NXDOMAIN
		m.NXDomain.Add(1)
	case 2: // SERVFAIL
		m.ServFail.Add(1)
		m.FailedQueries.Add(1)
	default:
		m.FailedQueries.Add(1)
	}

	m.TotalLatencyNs.Add(int64(latency))
	m.QueryCount.Add(1)

	// Record latency histogram
	ms := latency.Milliseconds()
	switch {
	case ms < 1:
		m.latencyBuckets[0].Add(1)
	case ms < 5:
		m.latencyBuckets[1].Add(1)
	case ms < 10:
		m.latencyBuckets[2].Add(1)
	case ms < 50:
		m.latencyBuckets[3].Add(1)
	case ms < 100:
		m.latencyBuckets[4].Add(1)
	case ms < 500:
		m.latencyBuckets[5].Add(1)
	case ms < 1000:
		m.latencyBuckets[6].Add(1)
	default:
		m.latencyBuckets[7].Add(1)
	}
}

// RecordQueryDetail records a detailed query log entry
func (m *Metrics) RecordQueryDetail(clientIP, domain, queryType, rcode, protocol string, latency time.Duration, cached bool) {
	entry := QueryLog{
		Timestamp:  time.Now(),
		ClientIP:   clientIP,
		Domain:     domain,
		QueryType:  queryType,
		Rcode:      rcode,
		LatencyMs:  float64(latency.Microseconds()) / 1000.0,
		Cached:     cached,
		Protocol:   protocol,
	}

	// Add to ring buffer
	m.queryLogMu.Lock()
	if len(m.queryLog) >= m.queryLogMax {
		m.queryLog = m.queryLog[1:]
	}
	m.queryLog = append(m.queryLog, entry)
	m.queryLogMu.Unlock()

	// Track domain counts
	m.domainCountsMu.Lock()
	dc, exists := m.domainCounts[domain]
	if !exists {
		dc = &domainCounter{}
		m.domainCounts[domain] = dc
	}
	dc.count++
	dc.totalLatNs += int64(latency)
	m.domainCountsMu.Unlock()
}

// GetRecentQueries returns the most recent query logs
func (m *Metrics) GetRecentQueries(limit int) []QueryLog {
	m.queryLogMu.RLock()
	defer m.queryLogMu.RUnlock()

	if limit <= 0 || limit > len(m.queryLog) {
		limit = len(m.queryLog)
	}

	// Return most recent entries
	start := len(m.queryLog) - limit
	result := make([]QueryLog, limit)
	copy(result, m.queryLog[start:])

	// Reverse so newest is first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

// GetTopDomains returns the most queried domains
func (m *Metrics) GetTopDomains(limit int) []DomainStat {
	m.domainCountsMu.RLock()
	defer m.domainCountsMu.RUnlock()

	// Collect all domains
	stats := make([]DomainStat, 0, len(m.domainCounts))
	for domain, dc := range m.domainCounts {
		avgLat := float64(0)
		if dc.count > 0 {
			avgLat = float64(dc.totalLatNs) / float64(dc.count) / 1e6
		}
		stats = append(stats, DomainStat{
			Domain:     domain,
			Count:      dc.count,
			AvgLatency: avgLat,
		})
	}

	// Sort by count (simple selection sort for top N)
	for i := 0; i < len(stats) && i < limit; i++ {
		maxIdx := i
		for j := i + 1; j < len(stats); j++ {
			if stats[j].Count > stats[maxIdx].Count {
				maxIdx = j
			}
		}
		stats[i], stats[maxIdx] = stats[maxIdx], stats[i]
	}

	if limit > len(stats) {
		limit = len(stats)
	}
	return stats[:limit]
}

// GetQPSHistory returns QPS data points for charting
func (m *Metrics) GetQPSHistory() []QPSPoint {
	m.qpsHistoryMu.RLock()
	defer m.qpsHistoryMu.RUnlock()

	result := make([]QPSPoint, len(m.qpsHistory))
	copy(result, m.qpsHistory)
	return result
}

// GetLatencyHistogram returns latency distribution
func (m *Metrics) GetLatencyHistogram() map[string]uint64 {
	return map[string]uint64{
		"<1ms":    m.latencyBuckets[0].Load(),
		"1-5ms":   m.latencyBuckets[1].Load(),
		"5-10ms":  m.latencyBuckets[2].Load(),
		"10-50ms": m.latencyBuckets[3].Load(),
		"50-100ms": m.latencyBuckets[4].Load(),
		"100-500ms": m.latencyBuckets[5].Load(),
		"500-1000ms": m.latencyBuckets[6].Load(),
		">1000ms": m.latencyBuckets[7].Load(),
	}
}

// trackQPS periodically calculates QPS and stores history
func (m *Metrics) trackQPS() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			currentCount := m.TotalQueries.Load()
			elapsed := now.Sub(m.lastQPSTime).Seconds()

			var qps float64
			if elapsed > 0 {
				qps = float64(currentCount-m.lastQPSCount) / elapsed
			}

			m.qpsHistoryMu.Lock()
			m.qpsHistory = append(m.qpsHistory, QPSPoint{
				Timestamp: now,
				QPS:       qps,
			})
			// Keep max 360 points (3 hours at 30s intervals)
			if len(m.qpsHistory) > 360 {
				m.qpsHistory = m.qpsHistory[1:]
			}
			m.qpsHistoryMu.Unlock()

			m.lastQPSCount = currentCount
			m.lastQPSTime = now

		case <-m.stopChan:
			return
		}
	}
}

// Snapshot returns a JSON-serializable snapshot of metrics
type Snapshot struct {
	Uptime         string  `json:"uptime"`
	UptimeSec      float64 `json:"uptime_seconds"`
	TotalQueries   uint64  `json:"total_queries"`
	UDPQueries     uint64  `json:"udp_queries"`
	TCPQueries     uint64  `json:"tcp_queries"`
	SuccessQueries uint64  `json:"success_queries"`
	FailedQueries  uint64  `json:"failed_queries"`
	NXDomain       uint64  `json:"nxdomain"`
	ServFail       uint64  `json:"servfail"`
	CacheSize      int     `json:"cache_size"`
	CacheHits      uint64  `json:"cache_hits"`
	CacheMisses    uint64  `json:"cache_misses"`
	CacheEvictions uint64  `json:"cache_evictions"`
	CacheHitRatio  float64 `json:"cache_hit_ratio"`
	RateLimited    uint64  `json:"rate_limited"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	QPS            float64 `json:"queries_per_second"`
	ActiveClients  int     `json:"active_clients"`
}

// GetSnapshot returns current metrics snapshot
func (m *Metrics) GetSnapshot() Snapshot {
	uptime := time.Since(m.startTime)
	totalQueries := m.TotalQueries.Load()
	queryCount := m.QueryCount.Load()

	var avgLatency float64
	if queryCount > 0 {
		avgLatency = float64(m.TotalLatencyNs.Load()) / float64(queryCount) / 1e6
	}

	var qps float64
	if uptime.Seconds() > 0 {
		qps = float64(totalQueries) / uptime.Seconds()
	}

	var cacheSize int
	var cacheHits, cacheMisses, cacheEvictions uint64
	if m.CacheStatsFunc != nil {
		cacheSize, cacheHits, cacheMisses, cacheEvictions = m.CacheStatsFunc()
	}

	var cacheHitRatio float64
	totalCacheOps := cacheHits + cacheMisses
	if totalCacheOps > 0 {
		cacheHitRatio = float64(cacheHits) / float64(totalCacheOps) * 100
	}

	var activeClients int
	if m.ActiveClientsFunc != nil {
		activeClients = m.ActiveClientsFunc()
	}

	return Snapshot{
		Uptime:         uptime.Round(time.Second).String(),
		UptimeSec:      uptime.Seconds(),
		TotalQueries:   totalQueries,
		UDPQueries:     m.UDPQueries.Load(),
		TCPQueries:     m.TCPQueries.Load(),
		SuccessQueries: m.SuccessQueries.Load(),
		FailedQueries:  m.FailedQueries.Load(),
		NXDomain:       m.NXDomain.Load(),
		ServFail:       m.ServFail.Load(),
		CacheSize:      cacheSize,
		CacheHits:      cacheHits,
		CacheMisses:    cacheMisses,
		CacheEvictions: cacheEvictions,
		CacheHitRatio:  cacheHitRatio,
		RateLimited:    m.RateLimited.Load(),
		AvgLatencyMs:   avgLatency,
		QPS:            qps,
		ActiveClients:  activeClients,
	}
}

// Stop stops the metrics goroutines
func (m *Metrics) Stop() {
	close(m.stopChan)
}
