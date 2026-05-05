package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"dns-server-mandiri/internal/metrics"
)

//go:embed static/*
var staticFiles embed.FS

// Dashboard serves the web monitoring dashboard
type Dashboard struct {
	metrics *metrics.Metrics
	logger  *slog.Logger
}

// New creates a new dashboard instance
func New(m *metrics.Metrics, logger *slog.Logger) *Dashboard {
	return &Dashboard{
		metrics: m,
		logger:  logger,
	}
}

// Handler returns the HTTP handler for the dashboard
func (d *Dashboard) Handler() http.Handler {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/stats", d.handleStats)
	mux.HandleFunc("/api/queries", d.handleQueries)
	mux.HandleFunc("/api/top-domains", d.handleTopDomains)
	mux.HandleFunc("/api/qps-history", d.handleQPSHistory)
	mux.HandleFunc("/api/latency-histogram", d.handleLatencyHistogram)
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
	json.NewEncoder(w).Encode(snapshot)
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

	history := d.metrics.GetQPSHistory()
	json.NewEncoder(w).Encode(history)
}

func (d *Dashboard) handleLatencyHistogram(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	histogram := d.metrics.GetLatencyHistogram()
	json.NewEncoder(w).Encode(histogram)
}

func (d *Dashboard) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
