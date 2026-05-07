package failover

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Config for failover
type Config struct {
	Enabled    bool          `yaml:"enabled"`
	Upstreams  []string      `yaml:"upstreams"`
	Timeout    time.Duration `yaml:"timeout"`
	MaxRetries int           `yaml:"max_retries"`
}

// DefaultFailoverConfig returns default failover configuration
func DefaultFailoverConfig() Config {
	return Config{
		Enabled:    true,
		Upstreams:  []string{"8.8.8.8", "1.1.1.1", "9.9.9.9"},
		Timeout:    3 * time.Second,
		MaxRetries: 2,
	}
}

// Failover handles DNS resolution fallback to upstream servers
type Failover struct {
	cfg     Config
	client  *dns.Client
	logger  *slog.Logger

	// Health tracking
	mu       sync.RWMutex
	healthy  map[string]bool
	lastCheck map[string]time.Time

	// Latency tracking per upstream
	latencyMu    sync.RWMutex
	latencyStats map[string]*UpstreamLatency

	stopChan chan struct{}
}

// UpstreamLatency tracks response time statistics for an upstream
type UpstreamLatency struct {
	TotalLatencyNs int64   `json:"-"`
	QueryCount     int64   `json:"-"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	LastLatencyMs  float64 `json:"last_latency_ms"`
}

// New creates a new failover handler
func New(cfg Config, logger *slog.Logger) *Failover {
	f := &Failover{
		cfg: cfg,
		client: &dns.Client{
			Net:     "udp",
			Timeout: cfg.Timeout,
		},
		logger:       logger,
		healthy:      make(map[string]bool),
		lastCheck:    make(map[string]time.Time),
		latencyStats: make(map[string]*UpstreamLatency),
		stopChan:     make(chan struct{}),
	}

	// Mark all upstreams as healthy initially
	for _, us := range cfg.Upstreams {
		f.healthy[us] = true
	}

	// Start health checker
	if cfg.Enabled {
		go f.healthCheckLoop()
	}

	return f
}

// Resolve attempts to resolve using upstream DNS servers (fallback)
func (f *Failover) Resolve(ctx context.Context, name string, qtype uint16) (*dns.Msg, error) {
	if !f.cfg.Enabled {
		return nil, nil
	}

	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), qtype)
	msg.RecursionDesired = true

	// Try healthy upstreams first
	upstreams := f.getOrderedUpstreams()

	for _, upstream := range upstreams {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		addr := upstream + ":53"
		for retry := 0; retry <= f.cfg.MaxRetries; retry++ {
			queryStart := time.Now()
			resp, _, err := f.client.ExchangeContext(ctx, msg, addr)
			latency := time.Since(queryStart)
			if err != nil {
				continue
			}

			if resp != nil && resp.Rcode != dns.RcodeServerFailure {
				f.markHealthy(upstream)
				f.recordLatency(upstream, latency)
				return resp, nil
			}
		}

		f.markUnhealthy(upstream)
	}

	return nil, nil
}

// getOrderedUpstreams returns upstreams with healthy ones first
func (f *Failover) getOrderedUpstreams() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var healthy, unhealthy []string
	for _, us := range f.cfg.Upstreams {
		if f.healthy[us] {
			healthy = append(healthy, us)
		} else {
			unhealthy = append(unhealthy, us)
		}
	}

	return append(healthy, unhealthy...)
}

func (f *Failover) markHealthy(upstream string) {
	f.mu.Lock()
	f.healthy[upstream] = true
	f.mu.Unlock()
}

func (f *Failover) markUnhealthy(upstream string) {
	f.mu.Lock()
	f.healthy[upstream] = false
	f.lastCheck[upstream] = time.Now()
	f.mu.Unlock()
	f.logger.Warn("upstream marked unhealthy", "upstream", upstream)
}

// healthCheckLoop periodically checks unhealthy upstreams
func (f *Failover) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			f.checkUnhealthy()
		case <-f.stopChan:
			return
		}
	}
}

// Stop stops the failover health check goroutine
func (f *Failover) Stop() {
	close(f.stopChan)
}

// recordLatency records response latency for an upstream
func (f *Failover) recordLatency(upstream string, latency time.Duration) {
	f.latencyMu.Lock()
	defer f.latencyMu.Unlock()

	stats, exists := f.latencyStats[upstream]
	if !exists {
		stats = &UpstreamLatency{}
		f.latencyStats[upstream] = stats
	}

	stats.TotalLatencyNs += int64(latency)
	stats.QueryCount++
	stats.LastLatencyMs = float64(latency.Microseconds()) / 1000.0
	if stats.QueryCount > 0 {
		stats.AvgLatencyMs = float64(stats.TotalLatencyNs) / float64(stats.QueryCount) / 1e6
	}
}

// GetLatencyStats returns latency statistics for all upstreams
func (f *Failover) GetLatencyStats() map[string]UpstreamLatency {
	f.latencyMu.RLock()
	defer f.latencyMu.RUnlock()

	result := make(map[string]UpstreamLatency)
	for k, v := range f.latencyStats {
		result[k] = *v
	}
	return result
}

func (f *Failover) checkUnhealthy() {
	f.mu.RLock()
	var toCheck []string
	for us, healthy := range f.healthy {
		if !healthy {
			toCheck = append(toCheck, us)
		}
	}
	f.mu.RUnlock()

	for _, us := range toCheck {
		msg := new(dns.Msg)
		msg.SetQuestion(".", dns.TypeNS)
		msg.RecursionDesired = true

		resp, _, err := f.client.Exchange(msg, us+":53")
		if err == nil && resp != nil && resp.Rcode == dns.RcodeSuccess {
			f.markHealthy(us)
			f.logger.Info("upstream recovered", "upstream", us)
		}
	}
}

// GetHealthStatus returns health status of all upstreams
func (f *Failover) GetHealthStatus() map[string]bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	status := make(map[string]bool)
	for k, v := range f.healthy {
		status[k] = v
	}
	return status
}

// IsEnabled returns whether failover is enabled
func (f *Failover) IsEnabled() bool {
	return f.cfg.Enabled
}
