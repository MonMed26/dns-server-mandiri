package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"dns-server-mandiri/internal/config"
	"dns-server-mandiri/internal/server"
)

var (
	version   = "1.0.0"
	buildTime = "dev"
)

func main() {
	// Parse flags
	configFile := flag.String("config", "", "Path to configuration file (YAML)")
	showVersion := flag.Bool("version", false, "Show version information")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	queryLog := flag.Bool("query-log", false, "Enable query logging")
	flag.Parse()

	if *showVersion {
		fmt.Printf("DNS Server Mandiri v%s (built: %s)\n", version, buildTime)
		os.Exit(0)
	}

	// Setup logger
	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	// Load configuration
	var cfg *config.Config
	var err error

	if *configFile != "" {
		cfg, err = config.LoadFromFile(*configFile)
		if err != nil {
			logger.Error("failed to load config", "file", *configFile, "error", err)
			os.Exit(1)
		}
		logger.Info("loaded configuration", "file", *configFile)
	} else {
		cfg = config.DefaultConfig()
		logger.Info("using default configuration")
	}

	// Override with flags
	if *queryLog {
		cfg.Logging.QueryLog = true
	}
	cfg.Logging.Level = *logLevel

	// Print startup banner
	logger.Info("=== DNS Server Mandiri ===",
		"version", version,
		"listen", fmt.Sprintf("%s:%d", cfg.Server.ListenAddr, cfg.Server.UDPPort),
		"cache_size", cfg.Cache.MaxSize,
		"workers", cfg.Server.Workers,
		"rate_limit", cfg.Rate.Enabled,
	)

	// Create and start server
	srv := server.New(cfg, logger)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("received signal, shutting down", "signal", sig)
		srv.Shutdown()
		os.Exit(0)
	}()

	// Start the server (blocks)
	if err := srv.Start(); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
