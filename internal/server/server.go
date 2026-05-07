package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"dns-server-mandiri/internal/cache"
	"dns-server-mandiri/internal/clientstats"
	"dns-server-mandiri/internal/config"
	"dns-server-mandiri/internal/dashboard"
	"dns-server-mandiri/internal/database"
	"dns-server-mandiri/internal/ecs"
	"dns-server-mandiri/internal/failover"
	"dns-server-mandiri/internal/filter"
	"dns-server-mandiri/internal/localrecords"
	"dns-server-mandiri/internal/metrics"
	"dns-server-mandiri/internal/persistence"
	"dns-server-mandiri/internal/ratelimit"
	"dns-server-mandiri/internal/resolver"

	"github.com/miekg/dns"
)

// Server is the main DNS server
type Server struct {
	cfg          *config.Config
	resolver     *resolver.Resolver
	cache        *cache.Cache
	limiter      *ratelimit.Limiter
	metrics      *metrics.Metrics
	filter       *filter.Filter
	failover     *failover.Failover
	persistence  *persistence.Persistence
	localRecords *localrecords.LocalRecords
	clientStats  *clientstats.Tracker
	ecs          *ecs.Handler
	dashboard    *dashboard.Dashboard
	db           *database.DB
	logger       *slog.Logger
	udpServer    *dns.Server
	tcpServer    *dns.Server
	prefetchStop chan struct{}
}

// New creates a new DNS server
func New(cfg *config.Config, logger *slog.Logger) *Server {
	// Initialize cache
	dnsCache := cache.New(
		cfg.Cache.MaxSize,
		cfg.Cache.MinTTL,
		cfg.Cache.MaxTTL,
		cfg.Cache.NegativeTTL,
		cfg.Cache.PrefetchRatio,
		cfg.Cache.CleanupInterval,
	)

	// Initialize rate limiter
	var limiter *ratelimit.Limiter
	if cfg.Rate.Enabled {
		limiter = ratelimit.New(
			cfg.Rate.RequestsPerSec,
			cfg.Rate.BurstSize,
			cfg.Rate.CleanupInterval,
		)
	}

	// Initialize metrics
	m := metrics.New()
	m.CacheStatsFunc = dnsCache.Stats
	if limiter != nil {
		m.ActiveClientsFunc = limiter.ActiveClients
	}

	// Initialize resolver
	res := resolver.New(dnsCache, cfg.Resolver, logger)

	// Initialize filter
	dnsFilter := filter.New(cfg.Filter, logger)

	// Initialize failover
	dnsFailover := failover.New(cfg.Failover, logger)

	// Initialize persistence
	persist := persistence.New(cfg.Persistence, logger)
	persist.GetEntriesFunc = func() []persistence.CacheEntry {
		exported := dnsCache.ExportEntries()
		entries := make([]persistence.CacheEntry, len(exported))
		for i, e := range exported {
			entries[i] = persistence.CacheEntry{
				Name:      e.Name,
				Qtype:     e.Qtype,
				Qclass:    e.Qclass,
				MsgBytes:  e.MsgBytes,
				ExpiresAt: e.ExpiresAt,
				CreatedAt: e.CreatedAt,
			}
		}
		return entries
	}

	// Initialize local records
	lr := localrecords.New(cfg.LocalRecords, logger)

	// Initialize client stats
	var cs *clientstats.Tracker
	if cfg.ClientStats.Enabled {
		cs = clientstats.New(cfg.ClientStats.MaxClients)
	}

	// Initialize ECS
	ecsHandler := ecs.New(cfg.ECS)

	// Initialize SQLite database
	dbPath := "/var/lib/dns-server/dns-server.db"
	if cfg.Persistence.FilePath != "" {
		// Use same directory as persistence file
		dbPath = cfg.Persistence.FilePath + ".db"
	}
	db, err := database.Open(dbPath, logger)
	if err != nil {
		logger.Error("failed to open database, admin features disabled", "error", err)
	}

	// Initialize dashboard
	dash := dashboard.New(m, logger)
	dash.SetFilter(dnsFilter)
	dash.SetLocalRecords(lr)
	dash.SetClientStats(cs)
	dash.SetFailover(dnsFailover)
	dash.SetAuth(cfg.DashboardAuth)
	if limiter != nil {
		dash.SetLimiter(limiter)
	}
	if db != nil {
		dash.SetDatabase(db)
	}

	// Load whitelist/blacklist from database into filter
	if db != nil && dnsFilter.IsEnabled() {
		if wl, err := db.GetWhitelistDomains(); err == nil {
			for _, d := range wl {
				dnsFilter.AddToWhitelist(d)
			}
		}
		if bl, err := db.GetBlacklistDomains(); err == nil {
			for _, d := range bl {
				dnsFilter.AddToBlacklist(d)
			}
		}
		// Load blocklist sources from DB
		if urls, err := db.GetEnabledBlocklistURLs(); err == nil && len(urls) > 0 {
			dnsFilter.SetSources(urls)
		}
	}

	return &Server{
		cfg:          cfg,
		resolver:     res,
		cache:        dnsCache,
		limiter:      limiter,
		metrics:      m,
		filter:       dnsFilter,
		failover:     dnsFailover,
		persistence:  persist,
		localRecords: lr,
		clientStats:  cs,
		db:           db,
		ecs:          ecsHandler,
		dashboard:    dash,
		logger:       logger,
		prefetchStop: make(chan struct{}),
	}
}

// Start starts the DNS server on both UDP and TCP
func (s *Server) Start() error {
	// Restore cache from disk
	if s.persistence.IsEnabled() {
		restored, err := s.persistence.Load(s.cache)
		if err != nil {
			s.logger.Warn("failed to restore cache", "error", err)
		} else if restored > 0 {
			s.logger.Info("cache restored from disk", "entries", restored)
		}
		s.persistence.StartAutoSave()
	}

	listenAddr := fmt.Sprintf("%s:%d", s.cfg.Server.ListenAddr, s.cfg.Server.UDPPort)

	// Setup DNS handler
	handler := dns.HandlerFunc(s.handleDNS)

	// UDP Server
	s.udpServer = &dns.Server{
		Addr:      listenAddr,
		Net:       "udp",
		Handler:   handler,
		UDPSize:   4096,
		ReusePort: true,
	}

	// TCP Server
	s.tcpServer = &dns.Server{
		Addr:    fmt.Sprintf("%s:%d", s.cfg.Server.ListenAddr, s.cfg.Server.TCPPort),
		Net:     "tcp",
		Handler: handler,
	}

	// Start dashboard HTTP server
	if s.cfg.Metrics.Enabled {
		go func() {
			err := s.dashboard.Serve(s.cfg.Metrics.ListenAddr, s.cfg.Metrics.Port)
			if err != nil {
				s.logger.Error("dashboard server failed", "error", err)
			}
		}()
		s.logger.Info("dashboard started",
			"url", fmt.Sprintf("http://%s:%d", s.cfg.Metrics.ListenAddr, s.cfg.Metrics.Port),
		)
	}

	// Start prefetch loop
	go s.prefetchLoop()

	// Cache warm-up: pre-resolve popular domains in background
	if s.cfg.CacheWarmup.Enabled && len(s.cfg.CacheWarmup.Domains) > 0 {
		go s.warmupCache()
	}

	// Log features status
	s.logger.Info("features status",
		"filter", s.filter.IsEnabled(),
		"failover", s.failover.IsEnabled(),
		"persistence", s.persistence.IsEnabled(),
		"local_records", s.localRecords.IsEnabled(),
		"ecs", s.ecs.IsEnabled(),
		"client_stats", s.cfg.ClientStats.Enabled,
	)

	// Start UDP server
	go func() {
		s.logger.Info("UDP DNS server starting", "addr", listenAddr)
		if err := s.udpServer.ListenAndServe(); err != nil {
			s.logger.Error("UDP server failed", "error", err)
		}
	}()

	// Start TCP server
	s.logger.Info("TCP DNS server starting", "addr", fmt.Sprintf("%s:%d", s.cfg.Server.ListenAddr, s.cfg.Server.TCPPort))
	if err := s.tcpServer.ListenAndServe(); err != nil {
		return fmt.Errorf("TCP server failed: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() {
	s.logger.Info("shutting down DNS server...")

	close(s.prefetchStop)

	if s.udpServer != nil {
		s.udpServer.Shutdown()
	}
	if s.tcpServer != nil {
		s.tcpServer.Shutdown()
	}
	if s.persistence != nil {
		s.persistence.Stop()
	}
	if s.cache != nil {
		s.cache.Stop()
	}
	if s.limiter != nil {
		s.limiter.Stop()
	}
	if s.failover != nil {
		s.failover.Stop()
	}
	if s.filter != nil {
		s.filter.Stop()
	}
	if s.metrics != nil {
		s.metrics.Stop()
	}
	if s.db != nil {
		s.db.Close()
	}

	s.logger.Info("DNS server stopped")
}

// handleDNS is the main DNS request handler
func (s *Server) handleDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()

	// Determine protocol
	protocol := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		protocol = "tcp"
	}

	// Get client IP
	clientIP := s.extractClientIP(w.RemoteAddr())

	// Rate limiting
	if s.limiter != nil && !s.limiter.Allow(clientIP) {
		s.metrics.RateLimited.Add(1)
		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeRefused)
		w.WriteMsg(resp)
		return
	}

	// Validate request
	if len(r.Question) == 0 {
		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeFormatError)
		w.WriteMsg(resp)
		return
	}

	q := r.Question[0]

	// Log query if enabled
	if s.cfg.Logging.QueryLog {
		s.logger.Info("query",
			"client", clientIP,
			"name", q.Name,
			"type", dns.TypeToString[q.Qtype],
			"protocol", protocol,
		)
	}

	// Step 1: Check DNS filter (ad blocking)
	if s.filter.IsBlocked(q.Name) {
		resp := s.filter.BlockedResponse(r, s.cfg.Filter.BlockResponse)
		latency := time.Since(start)
		s.metrics.RecordQuery(protocol, resp.Rcode, latency)
		s.metrics.RecordQueryDetail(clientIP, q.Name, dns.TypeToString[q.Qtype], "BLOCKED", protocol, latency, false)
		if s.clientStats != nil {
			s.clientStats.RecordQuery(clientIP, q.Name, true, latency)
		}
		w.WriteMsg(resp)
		return
	}

	// Step 2: Check local records
	if resp, found := s.localRecords.Lookup(q.Name, q.Qtype); found {
		resp.SetReply(r)
		latency := time.Since(start)
		s.metrics.RecordQuery(protocol, resp.Rcode, latency)
		s.metrics.RecordQueryDetail(clientIP, q.Name, dns.TypeToString[q.Qtype], "LOCAL", protocol, latency, false)
		if s.clientStats != nil {
			s.clientStats.RecordQuery(clientIP, q.Name, false, latency)
		}
		w.WriteMsg(resp)
		return
	}

	// Step 3: Resolve via recursive resolver (includes cache check internally)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, cached, err := s.resolver.Resolve(ctx, q.Name, q.Qtype, q.Qclass)

	// Step 4: If recursive fails, try failover
	if err != nil && s.failover.IsEnabled() {
		failoverResp, failoverErr := s.failover.Resolve(ctx, q.Name, q.Qtype)
		if failoverErr == nil && failoverResp != nil {
			resp = failoverResp
			err = nil
			s.logger.Debug("failover resolved", "name", q.Name, "type", dns.TypeToString[q.Qtype])
		}
	}

	latency := time.Since(start)

	if err != nil {
		s.logger.Debug("resolution failed",
			"name", q.Name,
			"type", dns.TypeToString[q.Qtype],
			"error", err,
			"latency", latency,
		)
		resp = new(dns.Msg)
		resp.SetRcode(r, dns.RcodeServerFailure)
		s.metrics.RecordQuery(protocol, dns.RcodeServerFailure, latency)
		s.metrics.RecordQueryDetail(clientIP, q.Name, dns.TypeToString[q.Qtype], "SERVFAIL", protocol, latency, false)
		if s.clientStats != nil {
			s.clientStats.RecordQuery(clientIP, q.Name, false, latency)
		}
		w.WriteMsg(resp)
		return
	}

	// Set response headers
	resp.SetReply(r)
	resp.RecursionAvailable = true
	resp.Compress = true

	// Record metrics
	s.metrics.RecordQuery(protocol, resp.Rcode, latency)
	rcodeStr := dns.RcodeToString[resp.Rcode]
	if rcodeStr == "" {
		rcodeStr = fmt.Sprintf("RCODE_%d", resp.Rcode)
	}
	s.metrics.RecordQueryDetail(clientIP, q.Name, dns.TypeToString[q.Qtype], rcodeStr, protocol, latency, cached)

	// Record client stats
	if s.clientStats != nil {
		s.clientStats.RecordQuery(clientIP, q.Name, false, latency)
	}

	// Send response
	if err := w.WriteMsg(resp); err != nil {
		s.logger.Error("failed to write response", "error", err, "client", clientIP)
	}
}

// extractClientIP extracts the IP address from a net.Addr
func (s *Server) extractClientIP(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.UDPAddr:
		return v.IP.String()
	case *net.TCPAddr:
		return v.IP.String()
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return strings.Split(addr.String(), ":")[0]
		}
		return host
	}
}

// prefetchLoop periodically prefetches popular cache entries
func (s *Server) prefetchLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.resolver.PrefetchPopular()
		case <-s.prefetchStop:
			return
		}
	}
}

// warmupCache pre-resolves popular domains to fill the cache after startup
func (s *Server) warmupCache() {
	s.logger.Info("cache warm-up starting", "domains", len(s.cfg.CacheWarmup.Domains))
	resolved := 0

	for _, domain := range s.cfg.CacheWarmup.Domains {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		// Resolve A record
		_, _, err := s.resolver.Resolve(ctx, domain, dns.TypeA, dns.ClassINET)
		if err == nil {
			resolved++
		}

		// Also resolve AAAA
		s.resolver.Resolve(ctx, domain, dns.TypeAAAA, dns.ClassINET)

		cancel()
	}

	s.logger.Info("cache warm-up completed", "resolved", resolved, "total", len(s.cfg.CacheWarmup.Domains))
}

// GetMetrics returns the metrics instance
func (s *Server) GetMetrics() *metrics.Metrics {
	return s.metrics
}
