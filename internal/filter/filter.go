package filter

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// BlockedDomainStat tracks how often a domain is blocked
type BlockedDomainStat struct {
	Domain string `json:"domain"`
	Count  uint64 `json:"count"`
}

// BlockList sources - popular ad/malware/tracking lists
var DefaultBlocklistURLs = []string{
	"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	"https://adaway.org/hosts.txt",
	"https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=0",
}

// Filter handles DNS-level domain blocking
type Filter struct {
	mu           sync.RWMutex
	blocked      map[string]struct{} // blocked domains (lowercase, FQDN)
	whitelist    map[string]struct{} // manually whitelisted domains
	blacklist    map[string]struct{} // manually blacklisted domains
	blocklistDir string
	sources      []string
	logger       *slog.Logger
	enabled      bool
	updateInterval time.Duration
	stopChan     chan struct{}

	// Stats
	TotalBlocked  atomic.Uint64
	TotalAllowed  atomic.Uint64
	BlocklistSize atomic.Int64

	// Top blocked domains tracking
	blockedCounts   map[string]uint64
	blockedCountsMu sync.RWMutex

	// Track if auto-update goroutine is running
	autoUpdateRunning bool
	autoUpdateMu      sync.Mutex
}

// Config for the filter
type Config struct {
	Enabled        bool          `yaml:"enabled"`
	BlocklistDir   string        `yaml:"blocklist_dir"`
	Sources        []string      `yaml:"sources"`
	WhitelistFile  string        `yaml:"whitelist_file"`
	BlacklistFile  string        `yaml:"blacklist_file"`
	UpdateInterval time.Duration `yaml:"update_interval"`
	BlockResponse  string        `yaml:"block_response"` // "zero" (0.0.0.0), "nxdomain", "refused"
}

// DefaultFilterConfig returns default filter configuration
func DefaultFilterConfig() Config {
	return Config{
		Enabled:        true,
		BlocklistDir:   "/var/lib/dns-server/blocklists",
		Sources:        DefaultBlocklistURLs,
		WhitelistFile:  "",
		BlacklistFile:  "",
		UpdateInterval: 24 * time.Hour,
		BlockResponse:  "zero",
	}
}

// New creates a new DNS filter
func New(cfg Config, logger *slog.Logger) *Filter {
	f := &Filter{
		blocked:        make(map[string]struct{}),
		whitelist:      make(map[string]struct{}),
		blacklist:      make(map[string]struct{}),
		blocklistDir:   cfg.BlocklistDir,
		sources:        cfg.Sources,
		logger:         logger,
		enabled:        cfg.Enabled,
		updateInterval: cfg.UpdateInterval,
		stopChan:       make(chan struct{}),
		blockedCounts:  make(map[string]uint64),
	}

	// Always create blocklist directory and load files, even if disabled.
	// This way when filter is enabled later via dashboard, data is ready.
	os.MkdirAll(cfg.BlocklistDir, 0755)

	// Load whitelist from file
	if cfg.WhitelistFile != "" {
		f.loadListFile(cfg.WhitelistFile, f.whitelist)
	}

	// Load blacklist from file
	if cfg.BlacklistFile != "" {
		f.loadListFile(cfg.BlacklistFile, f.blacklist)
	}

	// Load blocklists in background (non-blocking) — only if enabled AND has sources
	if f.enabled && len(f.sources) > 0 {
		go f.LoadBlocklists()
	}

	// Start auto-update (only if enabled)
	if f.enabled && cfg.UpdateInterval > 0 {
		f.startAutoUpdate()
	}

	return f
}

// IsBlocked checks if a domain should be blocked
func (f *Filter) IsBlocked(domain string) bool {
	if !f.enabled {
		return false
	}

	domain = strings.ToLower(dns.CanonicalName(domain))

	f.mu.RLock()
	defer f.mu.RUnlock()

	// Check whitelist first (always allow)
	if _, ok := f.whitelist[domain]; ok {
		f.TotalAllowed.Add(1)
		return false
	}

	// Check manual blacklist
	if _, ok := f.blacklist[domain]; ok {
		f.TotalBlocked.Add(1)
		f.recordBlocked(domain)
		return true
	}

	// Check blocklist (exact match)
	if _, ok := f.blocked[domain]; ok {
		f.TotalBlocked.Add(1)
		f.recordBlocked(domain)
		return true
	}

	// Check parent domains (wildcard blocking)
	parts := strings.Split(strings.TrimSuffix(domain, "."), ".")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[i:], ".") + "."
		if _, ok := f.blocked[parent]; ok {
			f.TotalBlocked.Add(1)
			f.recordBlocked(domain)
			return true
		}
	}

	f.TotalAllowed.Add(1)
	return false
}

// BlockedResponse creates a DNS response for blocked domains
func (f *Filter) BlockedResponse(r *dns.Msg, blockType string) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(r)
	resp.RecursionAvailable = true

	switch blockType {
	case "nxdomain":
		resp.Rcode = dns.RcodeNameError
	case "refused":
		resp.Rcode = dns.RcodeRefused
	default: // "zero" - return 0.0.0.0
		resp.Rcode = dns.RcodeSuccess
		if len(r.Question) > 0 {
			q := r.Question[0]
			switch q.Qtype {
			case dns.TypeA:
				resp.Answer = append(resp.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
					A:   net.ParseIP("0.0.0.0"),
				})
			case dns.TypeAAAA:
				resp.Answer = append(resp.Answer, &dns.AAAA{
					Hdr:  dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 300},
					AAAA: net.ParseIP("::"),
				})
			}
		}
	}

	return resp
}

// LoadBlocklists downloads and loads all blocklist sources
func (f *Filter) LoadBlocklists() {
	f.logger.Info("loading blocklists", "sources", len(f.sources))

	newBlocked := make(map[string]struct{})
	totalLoaded := 0

	for i, source := range f.sources {
		count, err := f.loadSource(source, newBlocked)
		if err != nil {
			f.logger.Error("failed to load blocklist", "source", source, "error", err)
			continue
		}
		totalLoaded += count
		f.logger.Info("loaded blocklist", "index", i+1, "source", truncateURL(source), "domains", count)
	}

	// Also load from local files in blocklist dir
	localCount := f.loadLocalFiles(newBlocked)
	totalLoaded += localCount

	f.mu.Lock()
	f.blocked = newBlocked
	f.mu.Unlock()

	f.BlocklistSize.Store(int64(totalLoaded))
	f.logger.Info("blocklists loaded", "total_domains", totalLoaded)
}

// loadSource downloads and parses a blocklist source
func (f *Filter) loadSource(source string, blocked map[string]struct{}) (int, error) {
	var reader io.Reader

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		// Download from URL
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(source)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		reader = resp.Body
	} else {
		// Load from file
		file, err := os.Open(source)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		reader = file
	}

	return f.parseHostsFile(reader, blocked), nil
}

// parseHostsFile parses a hosts-format blocklist
func (f *Filter) parseHostsFile(reader io.Reader, blocked map[string]struct{}) int {
	count := 0
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}

		// Parse hosts file format: "0.0.0.0 domain.com" or "127.0.0.1 domain.com"
		fields := strings.Fields(line)
		var domain string

		if len(fields) >= 2 && (fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1") {
			domain = fields[1]
		} else if len(fields) == 1 {
			// Plain domain list format
			domain = fields[0]
		} else {
			continue
		}

		// Clean domain
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || domain == "localhost" || domain == "localhost.localdomain" {
			continue
		}

		// Skip IP addresses
		if net.ParseIP(domain) != nil {
			continue
		}

		// Add trailing dot for FQDN
		if !strings.HasSuffix(domain, ".") {
			domain += "."
		}

		blocked[domain] = struct{}{}
		count++
	}

	return count
}

// loadLocalFiles loads blocklists from local directory
func (f *Filter) loadLocalFiles(blocked map[string]struct{}) int {
	count := 0
	entries, err := os.ReadDir(f.blocklistDir)
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(f.blocklistDir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		c := f.parseHostsFile(file, blocked)
		file.Close()
		count += c
	}

	return count
}

// loadListFile loads a whitelist or blacklist file
func (f *Filter) loadListFile(path string, list map[string]struct{}) {
	file, err := os.Open(path)
	if err != nil {
		f.logger.Warn("failed to load list file", "path", path, "error", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domain := strings.ToLower(line)
		if !strings.HasSuffix(domain, ".") {
			domain += "."
		}
		list[domain] = struct{}{}
	}
}

// AddToWhitelist adds a domain to the whitelist
func (f *Filter) AddToWhitelist(domain string) {
	domain = strings.ToLower(dns.CanonicalName(domain))
	f.mu.Lock()
	f.whitelist[domain] = struct{}{}
	f.mu.Unlock()
}

// RemoveFromWhitelist removes a domain from the whitelist
func (f *Filter) RemoveFromWhitelist(domain string) {
	domain = strings.ToLower(dns.CanonicalName(domain))
	f.mu.Lock()
	delete(f.whitelist, domain)
	f.mu.Unlock()
}

// AddToBlacklist adds a domain to the manual blacklist
func (f *Filter) AddToBlacklist(domain string) {
	domain = strings.ToLower(dns.CanonicalName(domain))
	f.mu.Lock()
	f.blacklist[domain] = struct{}{}
	f.mu.Unlock()
}

// RemoveFromBlacklist removes a domain from the manual blacklist
func (f *Filter) RemoveFromBlacklist(domain string) {
	domain = strings.ToLower(dns.CanonicalName(domain))
	f.mu.Lock()
	delete(f.blacklist, domain)
	f.mu.Unlock()
}

// GetWhitelist returns all whitelisted domains
func (f *Filter) GetWhitelist() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	list := make([]string, 0, len(f.whitelist))
	for d := range f.whitelist {
		list = append(list, d)
	}
	return list
}

// GetBlacklist returns all manually blacklisted domains
func (f *Filter) GetBlacklist() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	list := make([]string, 0, len(f.blacklist))
	for d := range f.blacklist {
		list = append(list, d)
	}
	return list
}

// Stats returns filter statistics
func (f *Filter) Stats() (blocked, allowed uint64, listSize int64) {
	return f.TotalBlocked.Load(), f.TotalAllowed.Load(), f.BlocklistSize.Load()
}

// recordBlocked tracks a blocked domain for top-blocked stats
func (f *Filter) recordBlocked(domain string) {
	f.blockedCountsMu.Lock()
	f.blockedCounts[domain]++
	// Cap at 10K unique blocked domains to prevent memory leak
	if len(f.blockedCounts) > 10000 {
		// Evict lowest count entry
		var lowestKey string
		var lowestCount uint64
		first := true
		for k, v := range f.blockedCounts {
			if first || v < lowestCount {
				lowestKey = k
				lowestCount = v
				first = false
			}
		}
		if lowestKey != "" {
			delete(f.blockedCounts, lowestKey)
		}
	}
	f.blockedCountsMu.Unlock()
}

// GetTopBlocked returns the most frequently blocked domains
func (f *Filter) GetTopBlocked(limit int) []BlockedDomainStat {
	f.blockedCountsMu.RLock()
	defer f.blockedCountsMu.RUnlock()

	stats := make([]BlockedDomainStat, 0, len(f.blockedCounts))
	for domain, count := range f.blockedCounts {
		stats = append(stats, BlockedDomainStat{Domain: domain, Count: count})
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})

	if limit > len(stats) {
		limit = len(stats)
	}
	return stats[:limit]
}

// startAutoUpdate starts the auto-update goroutine if not already running
func (f *Filter) startAutoUpdate() {
	f.autoUpdateMu.Lock()
	defer f.autoUpdateMu.Unlock()

	if f.autoUpdateRunning {
		return
	}
	f.autoUpdateRunning = true
	go f.autoUpdate()
}

// autoUpdate periodically refreshes blocklists
func (f *Filter) autoUpdate() {
	ticker := time.NewTicker(f.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if f.enabled {
				f.logger.Info("auto-updating blocklists")
				f.LoadBlocklists()
			}
		case <-f.stopChan:
			f.autoUpdateMu.Lock()
			f.autoUpdateRunning = false
			f.autoUpdateMu.Unlock()
			return
		}
	}
}

// Stop stops the filter
func (f *Filter) Stop() {
	close(f.stopChan)
}

// IsEnabled returns whether filtering is enabled
func (f *Filter) IsEnabled() bool {
	return f.enabled
}

// SetEnabled enables or disables filtering.
// When enabling, triggers blocklist loading if blocklists are empty.
func (f *Filter) SetEnabled(enabled bool) {
	wasEnabled := f.enabled
	f.enabled = enabled

	// If just enabled and blocklists are empty, load them
	if enabled && !wasEnabled {
		f.mu.RLock()
		blockedEmpty := len(f.blocked) == 0
		hasSources := len(f.sources) > 0
		f.mu.RUnlock()

		if blockedEmpty && hasSources {
			f.logger.Info("filter enabled, loading blocklists...")
			go f.LoadBlocklists()
		}

		// Start auto-update if not already running
		if f.updateInterval > 0 {
			f.startAutoUpdate()
		}
	}
}

// SetSources updates the blocklist source URLs
func (f *Filter) SetSources(sources []string) {
	f.mu.Lock()
	f.sources = sources
	f.mu.Unlock()
}

// ReloadIfNeeded triggers blocklist loading if blocklists are empty but sources exist
func (f *Filter) ReloadIfNeeded() {
	f.mu.RLock()
	blockedEmpty := len(f.blocked) == 0
	hasSources := len(f.sources) > 0
	f.mu.RUnlock()

	if blockedEmpty && hasSources {
		f.logger.Info("blocklists empty but sources available, loading...")
		go f.LoadBlocklists()
	}
}

func truncateURL(url string) string {
	if len(url) > 60 {
		return url[:60] + "..."
	}
	return url
}
