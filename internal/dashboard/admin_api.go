package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dns-server-mandiri/internal/database"
)

// SetDatabase sets the database reference for admin APIs
func (d *Dashboard) SetDatabase(db *database.DB) {
	d.db = db
}

// registerAdminRoutes registers all admin API routes
func (d *Dashboard) registerAdminRoutes(mux *http.ServeMux) {
	// Settings
	mux.HandleFunc("/api/admin/settings", d.handleAdminSettings)

	// Blocklist sources
	mux.HandleFunc("/api/admin/blocklist-sources", d.handleAdminBlocklistSources)
	mux.HandleFunc("/api/admin/blocklist-sources/toggle", d.handleAdminBlocklistToggle)
	mux.HandleFunc("/api/admin/blocklist-sources/reload", d.handleAdminBlocklistReload)

	// Whitelist
	mux.HandleFunc("/api/admin/whitelist", d.handleAdminWhitelist)

	// Blacklist
	mux.HandleFunc("/api/admin/blacklist", d.handleAdminBlacklist)

	// Local records
	mux.HandleFunc("/api/admin/local-records", d.handleAdminLocalRecords)
	mux.HandleFunc("/api/admin/local-records/toggle", d.handleAdminLocalRecordToggle)

	// Failover upstreams
	mux.HandleFunc("/api/admin/upstreams", d.handleAdminUpstreams)
	mux.HandleFunc("/api/admin/upstreams/toggle", d.handleAdminUpstreamToggle)
}

// === Settings ===
func (d *Dashboard) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		return
	}

	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	switch r.Method {
	case "GET":
		settings, err := d.db.GetAllSettings()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(settings)

	case "POST", "PUT":
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for key, value := range req {
			if err := d.db.SetSetting(key, value); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		// Apply settings live
		d.applySettings(req)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func (d *Dashboard) applySettings(settings map[string]string) {
	if v, ok := settings["filter_enabled"]; ok && d.filter != nil {
		d.filter.SetEnabled(v == "true")
	}
}

// === Blocklist Sources ===
func (d *Dashboard) handleAdminBlocklistSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	switch r.Method {
	case "GET":
		sources, err := d.db.GetBlocklistSources()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(sources)

	case "POST":
		var req struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Name == "" {
			req.Name = req.URL
		}
		id, err := d.db.AddBlocklistSource(req.Name, req.URL)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "added", "id": id})

	case "DELETE":
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		d.db.RemoveBlocklistSource(req.ID)
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	}
}

func (d *Dashboard) handleAdminBlocklistToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	var req struct {
		ID      int64 `json:"id"`
		Enabled bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	d.db.ToggleBlocklistSource(req.ID, req.Enabled)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (d *Dashboard) handleAdminBlocklistReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if d.filter != nil {
		// Get URLs from database
		if d.db != nil {
			urls, _ := d.db.GetEnabledBlocklistURLs()
			if len(urls) > 0 {
				d.filter.SetSources(urls)
			}
		}
		go d.filter.LoadBlocklists()
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "reloading"})
}

// === Whitelist ===
func (d *Dashboard) handleAdminWhitelist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	switch r.Method {
	case "GET":
		list, err := d.db.GetWhitelist()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(list)

	case "POST":
		var req struct {
			Domain string `json:"domain"`
			Note   string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		d.db.AddWhitelist(req.Domain, req.Note)
		// Also add to live filter
		if d.filter != nil {
			d.filter.AddToWhitelist(req.Domain)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "added"})

	case "DELETE":
		var req struct {
			ID     int64  `json:"id"`
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.ID > 0 {
			d.db.RemoveWhitelist(req.ID)
		} else if req.Domain != "" {
			d.db.RemoveWhitelistByDomain(req.Domain)
		}
		if d.filter != nil && req.Domain != "" {
			d.filter.RemoveFromWhitelist(req.Domain)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	}
}

// === Blacklist ===
func (d *Dashboard) handleAdminBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	switch r.Method {
	case "GET":
		list, err := d.db.GetBlacklist()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(list)

	case "POST":
		var req struct {
			Domain string `json:"domain"`
			Note   string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		d.db.AddBlacklist(req.Domain, req.Note)
		if d.filter != nil {
			d.filter.AddToBlacklist(req.Domain)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "added"})

	case "DELETE":
		var req struct {
			ID     int64  `json:"id"`
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.ID > 0 {
			d.db.RemoveBlacklist(req.ID)
		} else if req.Domain != "" {
			d.db.RemoveBlacklistByDomain(req.Domain)
		}
		if d.filter != nil && req.Domain != "" {
			d.filter.RemoveFromBlacklist(req.Domain)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	}
}

// === Local Records ===
func (d *Dashboard) handleAdminLocalRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	switch r.Method {
	case "GET":
		records, err := d.db.GetLocalRecords()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(records)

	case "POST":
		var req struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Value string `json:"value"`
			TTL   uint32 `json:"ttl"`
			Note  string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Value == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.TTL == 0 {
			req.TTL = 300
		}
		if req.Type == "" {
			req.Type = "A"
		}
		id, err := d.db.AddLocalRecord(req.Name, req.Type, req.Value, req.TTL, req.Note)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Add to live local records
		if d.localRecords != nil {
			d.localRecords.AddRecord(localRecordFromDB(req.Name, req.Type, req.Value, req.TTL))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "added", "id": id})

	case "DELETE":
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		d.db.RemoveLocalRecord(req.ID)
		// Note: live removal would need record details; reload is simpler
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	}
}

func (d *Dashboard) handleAdminLocalRecordToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	var req struct {
		ID      int64 `json:"id"`
		Enabled bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	d.db.ToggleLocalRecord(req.ID, req.Enabled)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// === Failover Upstreams ===
func (d *Dashboard) handleAdminUpstreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	switch r.Method {
	case "GET":
		upstreams, err := d.db.GetFailoverUpstreams()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(upstreams)

	case "POST":
		var req struct {
			Address  string `json:"address"`
			Priority int    `json:"priority"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Address == "" {
			http.Error(w, "invalid request", 400)
			return
		}
		id, err := d.db.AddFailoverUpstream(req.Address, req.Priority)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "added", "id": id})

	case "DELETE":
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		d.db.RemoveFailoverUpstream(req.ID)
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	}
}

func (d *Dashboard) handleAdminUpstreamToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if d.db == nil {
		http.Error(w, "database not available", 500)
		return
	}

	var req struct {
		ID      int64 `json:"id"`
		Enabled bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	d.db.ToggleFailoverUpstream(req.ID, req.Enabled)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// helper to get limit from query param
func getLimit(r *http.Request, defaultLimit int) int {
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultLimit
}
