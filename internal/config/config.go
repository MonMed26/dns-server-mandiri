package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the DNS server
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Cache    CacheConfig    `yaml:"cache"`
	Resolver ResolverConfig `yaml:"resolver"`
	Rate     RateConfig     `yaml:"rate"`
	Metrics  MetricsConfig  `yaml:"metrics"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	UDPPort    int    `yaml:"udp_port"`
	TCPPort    int    `yaml:"tcp_port"`
	Workers    int    `yaml:"workers"`
}

type CacheConfig struct {
	MaxSize       int           `yaml:"max_size"`
	DefaultTTL    time.Duration `yaml:"default_ttl"`
	MinTTL        time.Duration `yaml:"min_ttl"`
	MaxTTL        time.Duration `yaml:"max_ttl"`
	NegativeTTL   time.Duration `yaml:"negative_ttl"`
	PrefetchRatio float64       `yaml:"prefetch_ratio"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

type ResolverConfig struct {
	MaxDepth       int           `yaml:"max_depth"`
	MaxCNAMEChain  int           `yaml:"max_cname_chain"`
	Timeout        time.Duration `yaml:"timeout"`
	Retries        int           `yaml:"retries"`
	RootHintsFile  string        `yaml:"root_hints_file"`
	EnableDNSSEC   bool          `yaml:"enable_dnssec"`
}

type RateConfig struct {
	Enabled       bool          `yaml:"enabled"`
	RequestsPerSec int          `yaml:"requests_per_sec"`
	BurstSize     int           `yaml:"burst_size"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listen_addr"`
	Port       int    `yaml:"port"`
}

type LoggingConfig struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	QueryLog   bool   `yaml:"query_log"`
}

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
			PrefetchRatio:   0.1, // prefetch when 10% TTL remaining
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
