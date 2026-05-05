package database

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection
type DB struct {
	conn   *sql.DB
	logger *slog.Logger
}

// Open opens or creates the SQLite database
func Open(dbPath string, logger *slog.Logger) (*DB, error) {
	// Ensure directory exists
	os.MkdirAll(filepath.Dir(dbPath), 0755)

	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	// Set connection pool
	conn.SetMaxOpenConns(1) // SQLite only supports 1 writer
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

	db := &DB{conn: conn, logger: logger}

	// Run migrations
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}

	logger.Info("database opened", "path", dbPath)
	return db, nil
}

// Close closes the database
func (db *DB) Close() error {
	return db.conn.Close()
}

// migrate creates tables if they don't exist
func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS blocklist_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		url TEXT NOT NULL UNIQUE,
		enabled INTEGER NOT NULL DEFAULT 1,
		domain_count INTEGER DEFAULT 0,
		last_updated DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS whitelist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT NOT NULL UNIQUE,
		note TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS blacklist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT NOT NULL UNIQUE,
		note TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS local_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		value TEXT NOT NULL,
		ttl INTEGER DEFAULT 300,
		enabled INTEGER NOT NULL DEFAULT 1,
		note TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(name, type, value)
	);

	CREATE TABLE IF NOT EXISTS failover_upstreams (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		address TEXT NOT NULL UNIQUE,
		enabled INTEGER NOT NULL DEFAULT 1,
		priority INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Insert default settings if empty
	db.initDefaults()
	return nil
}

func (db *DB) initDefaults() {
	defaults := map[string]string{
		"filter_enabled":      "true",
		"filter_block_response": "zero",
		"failover_enabled":    "true",
		"cache_max_size":      "500000",
		"cache_min_ttl":       "30",
		"cache_max_ttl":       "86400",
		"cache_negative_ttl":  "300",
		"rate_limit_enabled":  "true",
		"rate_limit_per_sec":  "100",
		"rate_limit_burst":    "200",
		"ecs_enabled":         "true",
		"persistence_enabled": "true",
		"query_log_enabled":   "false",
		"blocklist_update_interval": "24",
	}

	for key, value := range defaults {
		db.conn.Exec("INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)", key, value)
	}

	// Insert default blocklist sources
	defaultSources := []struct {
		name string
		url  string
	}{
		{"StevenBlack Unified", "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"},
		{"AdAway", "https://adaway.org/hosts.txt"},
		{"Yoyo Ad Servers", "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=0"},
	}

	for _, s := range defaultSources {
		db.conn.Exec("INSERT OR IGNORE INTO blocklist_sources (name, url, enabled) VALUES (?, ?, 1)", s.name, s.url)
	}

	// Insert default failover upstreams
	defaultUpstreams := []string{"8.8.8.8", "1.1.1.1", "9.9.9.9"}
	for i, us := range defaultUpstreams {
		db.conn.Exec("INSERT OR IGNORE INTO failover_upstreams (address, enabled, priority) VALUES (?, 1, ?)", us, i)
	}
}

// === Settings ===

// GetSetting retrieves a setting value
func (db *DB) GetSetting(key string) (string, error) {
	var value string
	err := db.conn.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	return value, err
}

// SetSetting updates or inserts a setting
func (db *DB) SetSetting(key, value string) error {
	_, err := db.conn.Exec(
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP",
		key, value)
	return err
}

// GetAllSettings returns all settings as a map
func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.conn.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err == nil {
			settings[key] = value
		}
	}
	return settings, nil
}

// === Blocklist Sources ===

type BlocklistSource struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Enabled     bool      `json:"enabled"`
	DomainCount int       `json:"domain_count"`
	LastUpdated *time.Time `json:"last_updated"`
	CreatedAt   time.Time `json:"created_at"`
}

func (db *DB) GetBlocklistSources() ([]BlocklistSource, error) {
	rows, err := db.conn.Query("SELECT id, name, url, enabled, domain_count, last_updated, created_at FROM blocklist_sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []BlocklistSource
	for rows.Next() {
		var s BlocklistSource
		var enabled int
		var lastUpdated sql.NullTime
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &enabled, &s.DomainCount, &lastUpdated, &s.CreatedAt); err == nil {
			s.Enabled = enabled == 1
			if lastUpdated.Valid {
				s.LastUpdated = &lastUpdated.Time
			}
			sources = append(sources, s)
		}
	}
	return sources, nil
}

func (db *DB) GetEnabledBlocklistURLs() ([]string, error) {
	rows, err := db.conn.Query("SELECT url FROM blocklist_sources WHERE enabled = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err == nil {
			urls = append(urls, url)
		}
	}
	return urls, nil
}

func (db *DB) AddBlocklistSource(name, url string) (int64, error) {
	result, err := db.conn.Exec("INSERT INTO blocklist_sources (name, url, enabled) VALUES (?, ?, 1)", name, url)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) RemoveBlocklistSource(id int64) error {
	_, err := db.conn.Exec("DELETE FROM blocklist_sources WHERE id = ?", id)
	return err
}

func (db *DB) ToggleBlocklistSource(id int64, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := db.conn.Exec("UPDATE blocklist_sources SET enabled = ? WHERE id = ?", e, id)
	return err
}

func (db *DB) UpdateBlocklistSourceCount(id int64, count int) error {
	_, err := db.conn.Exec("UPDATE blocklist_sources SET domain_count = ?, last_updated = CURRENT_TIMESTAMP WHERE id = ?", count, id)
	return err
}

// === Whitelist ===

type DomainEntry struct {
	ID        int64     `json:"id"`
	Domain    string    `json:"domain"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

func (db *DB) GetWhitelist() ([]DomainEntry, error) {
	return db.getDomainList("whitelist")
}

func (db *DB) AddWhitelist(domain, note string) error {
	_, err := db.conn.Exec("INSERT OR IGNORE INTO whitelist (domain, note) VALUES (?, ?)", domain, note)
	return err
}

func (db *DB) RemoveWhitelist(id int64) error {
	_, err := db.conn.Exec("DELETE FROM whitelist WHERE id = ?", id)
	return err
}

func (db *DB) RemoveWhitelistByDomain(domain string) error {
	_, err := db.conn.Exec("DELETE FROM whitelist WHERE domain = ?", domain)
	return err
}

// === Blacklist ===

func (db *DB) GetBlacklist() ([]DomainEntry, error) {
	return db.getDomainList("blacklist")
}

func (db *DB) AddBlacklist(domain, note string) error {
	_, err := db.conn.Exec("INSERT OR IGNORE INTO blacklist (domain, note) VALUES (?, ?)", domain, note)
	return err
}

func (db *DB) RemoveBlacklist(id int64) error {
	_, err := db.conn.Exec("DELETE FROM blacklist WHERE id = ?", id)
	return err
}

func (db *DB) RemoveBlacklistByDomain(domain string) error {
	_, err := db.conn.Exec("DELETE FROM blacklist WHERE domain = ?", domain)
	return err
}

func (db *DB) getDomainList(table string) ([]DomainEntry, error) {
	rows, err := db.conn.Query("SELECT id, domain, note, created_at FROM " + table + " ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []DomainEntry
	for rows.Next() {
		var e DomainEntry
		if err := rows.Scan(&e.ID, &e.Domain, &e.Note, &e.CreatedAt); err == nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// GetWhitelistDomains returns just domain strings for the filter
func (db *DB) GetWhitelistDomains() ([]string, error) {
	rows, err := db.conn.Query("SELECT domain FROM whitelist")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil {
			domains = append(domains, d)
		}
	}
	return domains, nil
}

// GetBlacklistDomains returns just domain strings for the filter
func (db *DB) GetBlacklistDomains() ([]string, error) {
	rows, err := db.conn.Query("SELECT domain FROM blacklist")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil {
			domains = append(domains, d)
		}
	}
	return domains, nil
}

// === Local Records ===

type LocalRecord struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Value     string    `json:"value"`
	TTL       uint32    `json:"ttl"`
	Enabled   bool      `json:"enabled"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

func (db *DB) GetLocalRecords() ([]LocalRecord, error) {
	rows, err := db.conn.Query("SELECT id, name, type, value, ttl, enabled, note, created_at FROM local_records ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []LocalRecord
	for rows.Next() {
		var r LocalRecord
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.Value, &r.TTL, &enabled, &r.Note, &r.CreatedAt); err == nil {
			r.Enabled = enabled == 1
			records = append(records, r)
		}
	}
	return records, nil
}

func (db *DB) GetEnabledLocalRecords() ([]LocalRecord, error) {
	rows, err := db.conn.Query("SELECT id, name, type, value, ttl, enabled, note, created_at FROM local_records WHERE enabled = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []LocalRecord
	for rows.Next() {
		var r LocalRecord
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.Value, &r.TTL, &enabled, &r.Note, &r.CreatedAt); err == nil {
			r.Enabled = enabled == 1
			records = append(records, r)
		}
	}
	return records, nil
}

func (db *DB) AddLocalRecord(name, recordType, value string, ttl uint32, note string) (int64, error) {
	result, err := db.conn.Exec(
		"INSERT INTO local_records (name, type, value, ttl, enabled, note) VALUES (?, ?, ?, ?, 1, ?)",
		name, recordType, value, ttl, note)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) RemoveLocalRecord(id int64) error {
	_, err := db.conn.Exec("DELETE FROM local_records WHERE id = ?", id)
	return err
}

func (db *DB) ToggleLocalRecord(id int64, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := db.conn.Exec("UPDATE local_records SET enabled = ? WHERE id = ?", e, id)
	return err
}

// === Failover Upstreams ===

type FailoverUpstream struct {
	ID       int64  `json:"id"`
	Address  string `json:"address"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
}

func (db *DB) GetFailoverUpstreams() ([]FailoverUpstream, error) {
	rows, err := db.conn.Query("SELECT id, address, enabled, priority FROM failover_upstreams ORDER BY priority")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var upstreams []FailoverUpstream
	for rows.Next() {
		var u FailoverUpstream
		var enabled int
		if err := rows.Scan(&u.ID, &u.Address, &enabled, &u.Priority); err == nil {
			u.Enabled = enabled == 1
			upstreams = append(upstreams, u)
		}
	}
	return upstreams, nil
}

func (db *DB) GetEnabledUpstreams() ([]string, error) {
	rows, err := db.conn.Query("SELECT address FROM failover_upstreams WHERE enabled = 1 ORDER BY priority")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var addrs []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err == nil {
			addrs = append(addrs, a)
		}
	}
	return addrs, nil
}

func (db *DB) AddFailoverUpstream(address string, priority int) (int64, error) {
	result, err := db.conn.Exec("INSERT OR IGNORE INTO failover_upstreams (address, enabled, priority) VALUES (?, 1, ?)", address, priority)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) RemoveFailoverUpstream(id int64) error {
	_, err := db.conn.Exec("DELETE FROM failover_upstreams WHERE id = ?", id)
	return err
}

func (db *DB) ToggleFailoverUpstream(id int64, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := db.conn.Exec("UPDATE failover_upstreams SET enabled = ? WHERE id = ?", e, id)
	return err
}
