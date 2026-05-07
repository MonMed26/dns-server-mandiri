package config

import (
	"os"
	"time"

	"dns-server-mandiri/internal/clientstats"
	"dns-server-mandiri/internal/ecs"
	"dns-server-mandiri/internal/failover"
	"dns-server-mandiri/internal/filter"
	"dns-server-mandiri/internal/localrecords"
	"dns-server-mandiri/internal/persistence"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the DNS server
type Config struct {
	Server        ServerConfig          `yaml:"server"`
	Cache         CacheConfig           `yaml:"cache"`
	Resolver      ResolverConfig        `yaml:"resolver"`
	Rate          RateConfig            `yaml:"rate"`
	Metrics       MetricsConfig         `yaml:"metrics"`
	Logging       LoggingConfig         `yaml:"logging"`
	Filter        filter.Config         `yaml:"filter"`
	Failover      failover.Config       `yaml:"failover"`
	Persistence   persistence.Config    `yaml:"persistence"`
	LocalRecords  localrecords.Config   `yaml:"local_records"`
	ClientStats   ClientStatsConfig     `yaml:"client_stats"`
	ECS           ecs.Config            `yaml:"ecs"`
	DashboardAuth DashboardAuthConfig   `yaml:"dashboard_auth"`
	CacheWarmup   CacheWarmupConfig     `yaml:"cache_warmup"`
}

type ServerConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	UDPPort    int    `yaml:"udp_port"`
	TCPPort    int    `yaml:"tcp_port"`
	Workers    int    `yaml:"workers"`
}

type CacheConfig struct {
	MaxSize         int           `yaml:"max_size"`
	DefaultTTL      time.Duration `yaml:"default_ttl"`
	MinTTL          time.Duration `yaml:"min_ttl"`
	MaxTTL          time.Duration `yaml:"max_ttl"`
	NegativeTTL     time.Duration `yaml:"negative_ttl"`
	PrefetchRatio   float64       `yaml:"prefetch_ratio"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

type ResolverConfig struct {
	MaxDepth      int           `yaml:"max_depth"`
	MaxCNAMEChain int           `yaml:"max_cname_chain"`
	Timeout       time.Duration `yaml:"timeout"`
	Retries       int           `yaml:"retries"`
	RootHintsFile string        `yaml:"root_hints_file"`
	EnableDNSSEC  bool          `yaml:"enable_dnssec"`
}

type RateConfig struct {
	Enabled         bool          `yaml:"enabled"`
	RequestsPerSec  int           `yaml:"requests_per_sec"`
	BurstSize       int           `yaml:"burst_size"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listen_addr"`
	Port       int    `yaml:"port"`
}

type DashboardAuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type CacheWarmupConfig struct {
	Enabled bool     `yaml:"enabled"`
	Domains []string `yaml:"domains"`
}

type LoggingConfig struct {
	Level    string `yaml:"level"`
	File     string `yaml:"file"`
	QueryLog bool   `yaml:"query_log"`
}

type ClientStatsConfig struct {
	Enabled    bool `yaml:"enabled"`
	MaxClients int  `yaml:"max_clients"`
}

// Ensure clientstats is used
var _ = clientstats.New

// DefaultConfig returns a production-ready default configuration
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ListenAddr: "0.0.0.0",
			UDPPort:    53,
			TCPPort:    53,
			Workers:    4,
		},
		Cache: CacheConfig{
			MaxSize:         500000,
			DefaultTTL:      5 * time.Minute,
			MinTTL:          30 * time.Second,
			MaxTTL:          24 * time.Hour,
			NegativeTTL:     5 * time.Minute,
			PrefetchRatio:   0.1,
			CleanupInterval: 1 * time.Minute,
		},
		Resolver: ResolverConfig{
			MaxDepth:      30,
			MaxCNAMEChain: 10,
			Timeout:       2 * time.Second,
			Retries:       3,
			RootHintsFile: "",
			EnableDNSSEC:  false,
		},
		Rate: RateConfig{
			Enabled:         true,
			RequestsPerSec:  100,
			BurstSize:       200,
			CleanupInterval: 5 * time.Minute,
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			ListenAddr: "0.0.0.0",
			Port:       9153,
		},
		Logging: LoggingConfig{
			Level:    "info",
			File:     "",
			QueryLog: false,
		},
		Filter:       filter.DefaultFilterConfig(),
		Failover:     failover.DefaultFailoverConfig(),
		Persistence:  persistence.DefaultPersistenceConfig(),
		LocalRecords: localrecords.DefaultLocalRecordsConfig(),
		ClientStats: ClientStatsConfig{
			Enabled:    true,
			MaxClients: 10000,
		},
		ECS: ecs.DefaultECSConfig(),
		DashboardAuth: DashboardAuthConfig{
			Enabled:  false,
			Username: "admin",
			Password: "admin",
		},
		CacheWarmup: CacheWarmupConfig{
			Enabled: true,
			Domains: []string{
				"google.com", "www.google.com", "youtube.com", "www.youtube.com",
				"facebook.com", "www.facebook.com", "instagram.com", "www.instagram.com",
				"tiktok.com", "www.tiktok.com", "whatsapp.com", "web.whatsapp.com",
				"twitter.com", "x.com", "github.com", "cloudflare.com",
				"tokopedia.com", "shopee.co.id", "bukalapak.com", "gojek.com",
				"grab.com", "dana.id", "ovo.id", "bca.co.id",
				"detik.com", "kompas.com", "tribunnews.com", "liputan6.com",
			},
		},
	}
}

// LoadFromFile loads configuration from a YAML file
func LoadFromFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
