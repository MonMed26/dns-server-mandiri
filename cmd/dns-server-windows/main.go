//go:build windows

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"dns-server-mandiri/internal/config"
	"dns-server-mandiri/internal/server"

	"golang.org/x/sys/windows/svc"
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
	serviceCmd := flag.String("service", "", "Windows service command: install, uninstall, start, stop")
	flag.Parse()

	if *showVersion {
		fmt.Printf("DNS Server Mandiri v%s (built: %s) [Windows]\n", version, buildTime)
		os.Exit(0)
	}

	// Handle Windows service commands
	if *serviceCmd != "" {
		handleServiceCommand(*serviceCmd)
		return
	}

	// Check if running as Windows Service
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to detect service mode: %v\n", err)
		os.Exit(1)
	}

	if isService {
		runAsService(*configFile, *logLevel, *queryLog)
		return
	}

	// Running in console mode
	runConsole(*configFile, *logLevel, *queryLog)
}

func runConsole(configFile, logLevel string, queryLog bool) {
	logger := setupLogger(logLevel, "")

	cfg := loadConfig(configFile, logger)
	if queryLog {
		cfg.Logging.QueryLog = true
	}
	cfg.Logging.Level = logLevel

	logger.Info("=== DNS Server Mandiri (Windows) ===",
		"version", version,
		"mode", "console",
		"listen", fmt.Sprintf("%s:%d", cfg.Server.ListenAddr, cfg.Server.UDPPort),
		"dashboard", fmt.Sprintf("http://localhost:%d", cfg.Metrics.Port),
	)

	srv := server.New(cfg, logger)

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("received signal, shutting down", "signal", sig)
		srv.Shutdown()
		os.Exit(0)
	}()

	if err := srv.Start(); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func runAsService(configFile, logLevel string, queryLog bool) {
	// When running as service, use default config path if not specified
	if configFile == "" {
		exePath, _ := os.Executable()
		configFile = filepath.Join(filepath.Dir(exePath), "config.yaml")
	}

	err := svc.Run("DNSServerMandiri", &dnsService{
		configFile: configFile,
		logLevel:   logLevel,
		queryLog:   queryLog,
	})
	if err != nil {
		// Log to event log or stderr
		fmt.Fprintf(os.Stderr, "service failed: %v\n", err)
		os.Exit(1)
	}
}

func setupLogger(level string, logFile string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	var writer *os.File
	if logFile != "" {
		var err error
		writer, err = os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			writer = os.Stdout
		}
	} else {
		writer = os.Stdout
	}

	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: slogLevel,
	}))
}

func loadConfig(configFile string, logger *slog.Logger) *config.Config {
	var cfg *config.Config
	var err error

	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
		if err != nil {
			logger.Error("failed to load config", "file", configFile, "error", err)
			os.Exit(1)
		}
		logger.Info("loaded configuration", "file", configFile)
	} else {
		// Try default locations
		defaultPaths := []string{
			"config.yaml",
			filepath.Join(filepath.Dir(os.Args[0]), "config.yaml"),
			`C:\ProgramData\DNSServerMandiri\config.yaml`,
		}

		for _, p := range defaultPaths {
			if _, err := os.Stat(p); err == nil {
				cfg, err = config.LoadFromFile(p)
				if err == nil {
					logger.Info("loaded configuration", "file", p)
					break
				}
			}
		}

		if cfg == nil {
			cfg = config.DefaultConfig()
			logger.Info("using default configuration")
		}
	}

	return cfg
}
