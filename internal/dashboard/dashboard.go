package dashboard

import (
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"dns-server-mandiri/internal/clientstats"
	"dns-server-mandiri/internal/config"
	"dns-server-mandiri/internal/database"
	"dns-server-mandiri/internal/failover"
	"dns-server-mandiri/internal/filter"
	"dns-server-mandiri/internal/localrecords"
	"dns-server-mandiri/internal/metrics"
	"dns-server-mandiri/internal/ratelimit"
)

//go:embed static/*
var staticFiles embed.FS

// Dashboard serves the web monitoring dashboard
type Dashboard struct {
	metrics      *metrics.Metrics
	filter       *filter.Filter
	localRecords *localrecords.LocalRecords
	clientStats  *clientstats.Tracker
	failover     *failover.Failover
	limiter      *ratelimit.Limiter
	db           *database.DB
	logger       *slog.Logger
	authCfg      config.DashboardAuthConfig
}

// helper for admin_api to create local record
func localRecordFromDB(name, recordType, value string, ttl uint32) localrecords.Record {
	return localrecords.Record{Name: name, Type: recordType, Value: value, TTL: ttl}
}

// New creates a new dashboard instance
func New(m *metrics.Metrics, logger *slog.Logger) *Dashboard {
	return &Dashboard{
		metrics: m,
		logger:  logger,
	}
}

// SetFilter sets the filter reference
func (d *Dashboard) SetFilter(f *filter.Filter) {
	d.filter = f
}

// SetLocalRecords sets the local records reference
func (d *Dashboard) SetLocalRecords(lr *localrecords.LocalRecords) {
	d.localRecords = lr
}

// SetClientStats sets the client stats reference
func (d *Dashboard) SetClientStats(cs *clientstats.Tracker) {
	d.clientStats = cs
}

// SetFailover sets the failover reference
func (d *Dashboard) SetFailover(f *failover.Failover) {
	d.failover = f
}

// SetLimiter sets the rate limiter reference
func (d *Dashboard) SetLimiter(l *ratelimit.Limiter) {
	d.limiter = l
}

// SetAuth sets the dashboard authentication config
func (d *Dashboard) SetAuth(cfg config.DashboardAuthConfig) {
	d.authCfg = cfg
}

// basicAuth is a middleware that enforces HTTP Basic Authentication
func (d *Dashboard) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.authCfg.Enabled {
			next(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(d.authCfg.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(d.authCfg.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="DNS Server Mandiri"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// Handler returns the HTTP handler for the dashboard
func (d *Dashboard) Handler() http.Handler {
	mux := http.NewServeMux()

	// Core API endpoints (protected)
	mux.HandleFunc("/api/stats", d.basicAuth(d.handleStats))
	mux.HandleFunc("/api/queries", d.basicAuth(d.handleQueries))
	mux.HandleFunc("/api/queries/export", d.basicAuth(d.handleQueriesExport))
	mux.HandleFunc("/api/top-domains", d.basicAuth(d.handleTopDomains))
	mux.HandleFunc("/api/qps-history", d.basicAuth(d.handleQPSHistory))
	mux.HandleFunc("/api/latency-histogram", d.basicAuth(d.handleLatencyHistogram))

	// Filter API endpoints (protected)
	mux.HandleFunc("/api/filter/stats", d.basicAuth(d.handleFilterStats))
	mux.HandleFunc("/api/filter/whitelist", d.basicAuth(d.handleFilterWhitelist))
	mux.HandleFunc("/api/filter/blacklist", d.basicAuth(d.handleFilterBlacklist))
	mux.HandleFunc("/api/filter/toggle", d.basicAuth(d.handleFilterToggle))
	mux.HandleFunc("/api/filter/reload", d.basicAuth(d.handleFilterReload))
	mux.HandleFunc("/api/filter/top-blocked", d.basicAuth(d.handleTopBlocked))

	// Local records API (protected)
	mux.HandleFunc("/api/local-records", d.basicAuth(d.handleLocalRecords))

	// Client stats API (protected)
	mux.HandleFunc("/api/clients", d.basicAuth(d.handleClients))

	// Failover API (protected)
	mux.HandleFunc("/api/failover/status", d.basicAuth(d.handleFailoverStatus))
	mux.HandleFunc("/api/failover/latency", d.basicAuth(d.handleFailoverLatency))

	// Rate limit stats API (protected)
	mux.HandleFunc("/api/ratelimit/stats", d.basicAuth(d.handleRateLimitStats))

	// Admin API (SQLite-backed, protected)
	d.registerAdminRoutes(mux)

	// Health (unprotected - for monitoring)
	mux.HandleFunc("/health", d.handleHealth)

	// Serve embedded static files (protected)
	mux.Handle("/static/", d.basicAuthHandler(http.FileServer(http.FS(staticFiles))))

	// Serve index.html at root (protected)
	mux.HandleFunc("/", d.basicAuth(d.handleIndex))

	return mux
}

// basicAuthHandler wraps an http.Handler with basic auth
func (d *Dashboard) basicAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.authCfg.Enabled {
			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(d.authCfg.Username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(d.authCfg.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="DNS Server Mandiri"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Serve starts the dashboard HTTP server
func (d *Dashboard) Serve(addr string, port int) error {
	listenAddr := fmt.Sprintf("%s:%d", addr, port)
	d.logger.Info("dashboard starting", "addr", listenAddr)
	return http.ListenAndServe(listenAddr, d.Handler())
}

func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (d *Dashboard) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	snapshot := d.metrics.GetSnapshot()

	// Add filter stats
	type extendedStats struct {
		metrics.Snapshot
		FilterEnabled  bool   `json:"filter_enabled"`
		FilterBlocked  uint64 `json:"filter_blocked"`
		FilterAllowed  uint64 `json:"filter_allowed"`
		BlocklistSize  int64  `json:"blocklist_size"`
		TotalClients   int    `json:"total_clients"`
	}

	ext := extendedStats{Snapshot: snapshot}
	if d.filter != nil {
		ext.FilterEnabled = d.filter.IsEnabled()
		blocked, allowed, listSize := d.filter.Stats()
		ext.FilterBlocked = blocked
		ext.FilterAllowed = allowed
		ext.BlocklistSize = listSize
	}
	if d.clientStats != nil {
		ext.TotalClients = d.clientStats.TotalClients()
	}

	json.NewEncoder(w).Encode(ext)
}

func (d *Dashboard) handleQueries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	queries := d.metrics.GetRecentQueries(limit)
	json.NewEncoder(w).Encode(queries)
}

func (d *Dashboard) handleTopDomains(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	domains := d.metrics.GetTopDomains(limit)
	json.NewEncoder(w).Encode(domains)
}

func (d *Dashboard) handleQPSHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(d.metrics.GetQPSHistory())
}

func (d *Dashboard) handleLatencyHistogram(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(d.metrics.GetLatencyHistogram())
}

// Filter handlers
func (d *Dashboard) handleFilterStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false})
		return
	}

	blocked, allowed, listSize := d.filter.Stats()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":       d.filter.IsEnabled(),
		"blocked":       blocked,
		"allowed":       allowed,
		"blocklist_size": listSize,
	})
}

func (d *Dashboard) handleFilterWhitelist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil {
		http.Error(w, "filter not enabled", 400)
		return
	}

	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(d.filter.GetWhitelist())
	case "POST":
		var req struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		d.filter.AddToWhitelist(req.Domain)
		json.NewEncoder(w).Encode(map[string]string{"status": "added", "domain": req.Domain})
	case "DELETE":
		var req struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		d.filter.RemoveFromWhitelist(req.Domain)
		json.NewEncoder(w).Encode(map[string]string{"status": "removed", "domain": req.Domain})
	}
}

func (d *Dashboard) handleFilterBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil {
		http.Error(w, "filter not enabled", 400)
		return
	}

	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(d.filter.GetBlacklist())
	case "POST":
		var req struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		d.filter.AddToBlacklist(req.Domain)
		json.NewEncoder(w).Encode(map[string]string{"status": "added", "domain": req.Domain})
	case "DELETE":
		var req struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		d.filter.RemoveFromBlacklist(req.Domain)
		json.NewEncoder(w).Encode(map[string]string{"status": "removed", "domain": req.Domain})
	}
}

func (d *Dashboard) handleFilterToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil {
		http.Error(w, "filter not available", 400)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	d.filter.SetEnabled(req.Enabled)
	// Persist to database
	if d.db != nil {
		enabledStr := "false"
		if req.Enabled {
			enabledStr = "true"
		}
		d.db.SetSetting("filter_enabled", enabledStr)
	}
	json.NewEncoder(w).Encode(map[string]bool{"enabled": req.Enabled})
}

func (d *Dashboard) handleFilterReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil || r.Method != "POST" {
		http.Error(w, "not available", 400)
		return
	}

	// Sync sources from DB before reloading
	if d.db != nil {
		urls, _ := d.db.GetEnabledBlocklistURLs()
		if len(urls) > 0 {
			d.filter.SetSources(urls)
		}
	}

	go d.filter.LoadBlocklists()
	json.NewEncoder(w).Encode(map[string]string{"status": "reloading"})
}

// Local records handler
func (d *Dashboard) handleLocalRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.localRecords == nil {
		http.Error(w, "local records not enabled", 400)
		return
	}

	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(d.localRecords.GetAllRecords())
	case "POST":
		var rec localrecords.Record
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, "invalid request: "+err.Error(), 400)
			return
		}
		if err := d.localRecords.AddRecord(rec); err != nil {
			http.Error(w, "failed to add record: "+err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "added"})
	case "DELETE":
		var req struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		d.localRecords.RemoveRecord(req.Name, req.Type)
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	}
}

// Client stats handler
func (d *Dashboard) handleClients(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.clientStats == nil {
		json.NewEncoder(w).Encode([]struct{}{})
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// Check if specific client requested
	if ip := r.URL.Query().Get("ip"); ip != "" {
		client := d.clientStats.GetClient(ip)
		if client == nil {
			http.Error(w, "client not found", 404)
			return
		}
		json.NewEncoder(w).Encode(client)
		return
	}

	clients := d.clientStats.GetTopClients(limit)
	json.NewEncoder(w).Encode(clients)
}

// Failover status handler
func (d *Dashboard) handleFailoverStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.failover == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":   d.failover.IsEnabled(),
		"upstreams": d.failover.GetHealthStatus(),
	})
}

func (d *Dashboard) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleQueriesExport exports query logs as CSV or JSON
func (d *Dashboard) handleQueriesExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	logs := d.metrics.GetAllQueryLogs()

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=dns-queries-"+time.Now().Format("20060102-150405")+".csv")
		writer := csv.NewWriter(w)
		writer.Write([]string{"timestamp", "client_ip", "domain", "query_type", "rcode", "latency_ms", "cached", "protocol"})
		for _, q := range logs {
			cached := "false"
			if q.Cached {
				cached = "true"
			}
			writer.Write([]string{
				q.Timestamp.Format(time.RFC3339),
				q.ClientIP,
				q.Domain,
				q.QueryType,
				q.Rcode,
				fmt.Sprintf("%.2f", q.LatencyMs),
				cached,
				q.Protocol,
			})
		}
		writer.Flush()

	default: // json
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=dns-queries-"+time.Now().Format("20060102-150405")+".json")
		json.NewEncoder(w).Encode(logs)
	}
}

// handleTopBlocked returns the most frequently blocked domains
func (d *Dashboard) handleTopBlocked(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil {
		json.NewEncoder(w).Encode([]struct{}{})
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	topBlocked := d.filter.GetTopBlocked(limit)
	json.NewEncoder(w).Encode(topBlocked)
}

// handleFailoverLatency returns latency stats for upstream DNS servers
func (d *Dashboard) handleFailoverLatency(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.failover == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{})
		return
	}

	json.NewEncoder(w).Encode(d.failover.GetLatencyStats())
}

// handleRateLimitStats returns rate limiting statistics
func (d *Dashboard) handleRateLimitStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.limiter == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false})
		return
	}

	stats := d.limiter.GetAllStats()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":        true,
		"active_clients": len(stats),
		"clients":        stats,
	})
}
