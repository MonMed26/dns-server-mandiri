package persistence

import (
	"encoding/gob"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// CacheEntry represents a serializable cache entry
type CacheEntry struct {
	Name      string
	Qtype     uint16
	Qclass    uint16
	MsgBytes  []byte
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Config for persistence
type Config struct {
	Enabled      bool          `yaml:"enabled"`
	FilePath     string        `yaml:"file_path"`
	SaveInterval time.Duration `yaml:"save_interval"`
}

// DefaultPersistenceConfig returns default persistence configuration
func DefaultPersistenceConfig() Config {
	return Config{
		Enabled:      true,
		FilePath:     "/var/lib/dns-server/cache.gob",
		SaveInterval: 5 * time.Minute,
	}
}

// CacheStore interface for the cache
type CacheStore interface {
	Set(name string, qtype uint16, qclass uint16, msg *dns.Msg)
}

// Persistence handles saving and loading cache to/from disk
type Persistence struct {
	cfg      Config
	logger   *slog.Logger
	mu       sync.Mutex
	stopChan chan struct{}

	// Function to get all cache entries for saving
	GetEntriesFunc func() []CacheEntry
}

// New creates a new persistence handler
func New(cfg Config, logger *slog.Logger) *Persistence {
	if cfg.FilePath != "" {
		os.MkdirAll(filepath.Dir(cfg.FilePath), 0755)
	}

	return &Persistence{
		cfg:      cfg,
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

// StartAutoSave starts periodic cache saving
func (p *Persistence) StartAutoSave() {
	if !p.cfg.Enabled || p.cfg.SaveInterval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(p.cfg.SaveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := p.Save(); err != nil {
					p.logger.Error("failed to save cache", "error", err)
				}
			case <-p.stopChan:
				// Final save on shutdown
				p.Save()
				return
			}
		}
	}()
}

// Save writes cache entries to disk
func (p *Persistence) Save() error {
	if !p.cfg.Enabled || p.GetEntriesFunc == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	entries := p.GetEntriesFunc()
	if len(entries) == 0 {
		return nil
	}

	// Filter out expired entries
	now := time.Now()
	var valid []CacheEntry
	for _, e := range entries {
		if e.ExpiresAt.After(now) {
			valid = append(valid, e)
		}
	}

	// Write to temp file first, then rename (atomic)
	tmpPath := p.cfg.FilePath + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	encoder := gob.NewEncoder(file)
	err = encoder.Encode(valid)
	file.Close()

	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, p.cfg.FilePath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	p.logger.Debug("cache saved to disk", "entries", len(valid), "path", p.cfg.FilePath)
	return nil
}

// Load reads cache entries from disk and restores them
func (p *Persistence) Load(cache CacheStore) (int, error) {
	if !p.cfg.Enabled {
		return 0, nil
	}

	file, err := os.Open(p.cfg.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // No cache file yet
		}
		return 0, err
	}
	defer file.Close()

	var entries []CacheEntry
	decoder := gob.NewDecoder(file)
	if err := decoder.Decode(&entries); err != nil {
		return 0, err
	}

	// Restore valid entries
	now := time.Now()
	restored := 0
	for _, e := range entries {
		if e.ExpiresAt.After(now) {
			msg := new(dns.Msg)
			if err := msg.Unpack(e.MsgBytes); err == nil {
				cache.Set(e.Name, e.Qtype, e.Qclass, msg)
				restored++
			}
		}
	}

	p.logger.Info("cache restored from disk", "restored", restored, "total", len(entries))
	return restored, nil
}

// Stop stops the persistence handler
func (p *Persistence) Stop() {
	close(p.stopChan)
}

// IsEnabled returns whether persistence is enabled
func (p *Persistence) IsEnabled() bool {
	return p.cfg.Enabled
}
