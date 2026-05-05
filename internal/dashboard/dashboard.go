package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"dns-server-mandiri/internal/clientstats"
	"dns-server-mandiri/internal/database"
	"dns-server-mandiri/internal/failover"
	"dns-server-mandiri/internal/filter"
	"dns-server-mandiri/internal/localrecords"
	"dns-server-mandiri/internal/metrics"
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
	db           *database.DB
	logger       *slog.Logger
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

// Handler returns the HTTP handler for the dashboard
func (d *Dashboard) Handler() http.Handler {
	mux := http.NewServeMux()

	// Core API endpoints
	mux.HandleFunc("/api/stats", d.handleStats)
	mux.HandleFunc("/api/queries", d.handleQueries)
	mux.HandleFunc("/api/top-domains", d.handleTopDomains)
	mux.HandleFunc("/api/qps-history", d.handleQPSHistory)
	mux.HandleFunc("/api/latency-histogram", d.handleLatencyHistogram)

	// Filter API endpoints
	mux.HandleFunc("/api/filter/stats", d.handleFilterStats)
	mux.HandleFunc("/api/filter/whitelist", d.handleFilterWhitelist)
	mux.HandleFunc("/api/filter/blacklist", d.handleFilterBlacklist)
	mux.HandleFunc("/api/filter/toggle", d.handleFilterToggle)
	mux.HandleFunc("/api/filter/reload", d.handleFilterReload)

	// Local records API
	mux.HandleFunc("/api/local-records", d.handleLocalRecords)

	// Client stats API
	mux.HandleFunc("/api/clients", d.handleClients)

	// Failover API
	mux.HandleFunc("/api/failover/status", d.handleFailoverStatus)

	// Admin API (SQLite-backed)
	d.registerAdminRoutes(mux)

	// Health
	mux.HandleFunc("/health", d.handleHealth)

	// Serve embedded static files
	mux.Handle("/static/", http.FileServer(http.FS(staticFiles)))

	// Serve index.html at root
	mux.HandleFunc("/", d.handleIndex)

	return mux
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
	json.NewEncoder(w).Encode(map[string]bool{"enabled": req.Enabled})
}

func (d *Dashboard) handleFilterReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if d.filter == nil || r.Method != "POST" {
		http.Error(w, "not available", 400)
		return
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
