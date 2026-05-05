package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"dns-server-mandiri/internal/cache"
	"dns-server-mandiri/internal/config"
	"dns-server-mandiri/internal/dashboard"
	"dns-server-mandiri/internal/metrics"
	"dns-server-mandiri/internal/ratelimit"
	"dns-server-mandiri/internal/resolver"

	"github.com/miekg/dns"
)

// Server is the main DNS server
type Server struct {
	cfg       *config.Config
	resolver  *resolver.Resolver
	cache     *cache.Cache
	limiter   *ratelimit.Limiter
	metrics   *metrics.Metrics
	logger    *slog.Logger
	udpServer *dns.Server
	tcpServer *dns.Server
	dashboard *dashboard.Dashboard
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

	// Initialize dashboard
	dash := dashboard.New(m, logger)

	return &Server{
		cfg:          cfg,
		resolver:     res,
		cache:        dnsCache,
		limiter:      limiter,
		metrics:      m,
		logger:       logger,
		dashboard:    dash,
		prefetchStop: make(chan struct{}),
	}
}

// Start starts the DNS server on both UDP and TCP
func (s *Server) Start() error {
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

	// Start dashboard HTTP server (includes metrics API)
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
	if s.cache != nil {
		s.cache.Stop()
	}
	if s.limiter != nil {
		s.limiter.Stop()
	}
	if s.metrics != nil {
		s.metrics.Stop()
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

	// Get client IP for rate limiting
	clientIP := s.extractClientIP(w.RemoteAddr())

	// Rate limiting
	if s.limiter != nil && !s.limiter.Allow(clientIP) {
		s.metrics.RateLimited.Add(1)
		s.logger.Debug("rate limited", "client", clientIP)
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

	// Check if result is from cache (for metrics)
	_, cached, _ := s.cache.Get(q.Name, q.Qtype, q.Qclass)

	// Resolve the query
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := s.resolver.Resolve(ctx, q.Name, q.Qtype, q.Qclass)

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
		w.WriteMsg(resp)
		return
	}

	// Set response headers
	resp.SetReply(r)
	resp.RecursionAvailable = true
	resp.Compress = true

	// Record metrics
	s.metrics.RecordQuery(protocol, resp.Rcode, latency)

	// Record detailed query log
	rcodeStr := dns.RcodeToString[resp.Rcode]
	if rcodeStr == "" {
		rcodeStr = fmt.Sprintf("RCODE_%d", resp.Rcode)
	}
	s.metrics.RecordQueryDetail(clientIP, q.Name, dns.TypeToString[q.Qtype], rcodeStr, protocol, latency, cached)

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
		// Fallback: parse from string
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

// GetMetrics returns the metrics instance
func (s *Server) GetMetrics() *metrics.Metrics {
	return s.metrics
}
