//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dns-server-mandiri/internal/server"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "DNSServerMandiri"
const serviceDisplayName = "DNS Server Mandiri"
const serviceDescription = "Production-grade recursive DNS server with web dashboard"

// dnsService implements svc.Handler
type dnsService struct {
	configFile string
	logLevel   string
	queryLog   bool
}

func (s *dnsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	changes <- svc.Status{State: svc.StartPending}

	// Setup logger (log to file when running as service)
	logDir := `C:\ProgramData\DNSServerMandiri\logs`
	os.MkdirAll(logDir, 0755)
	logFile := filepath.Join(logDir, "dns-server.log")

	logger := setupLogger(s.logLevel, logFile)
	logger.Info("service starting", "version", version)

	// Load config
	cfg := loadConfig(s.configFile, logger)
	if s.queryLog {
		cfg.Logging.QueryLog = true
	}

	// Create server
	srv := server.New(cfg, logger)

	// Start server in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	logger.Info("service running",
		"dns", fmt.Sprintf("%s:%d", cfg.Server.ListenAddr, cfg.Server.UDPPort),
		"dashboard", fmt.Sprintf("http://localhost:%d", cfg.Metrics.Port),
	)

	// Wait for stop signal or error
	for {
		select {
		case err := <-errChan:
			if err != nil {
				logger.Error("server error", "error", err)
				return false, 1
			}
			return false, 0

		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus

			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				logger.Info("service stopping")
				srv.Shutdown()
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

// handleServiceCommand handles install/uninstall/start/stop commands
func handleServiceCommand(cmd string) {
	switch cmd {
	case "install":
		installService()
	case "uninstall":
		uninstallService()
	case "start":
		startService()
	case "stop":
		stopService()
	default:
		fmt.Fprintf(os.Stderr, "Unknown service command: %s\n", cmd)
		fmt.Fprintf(os.Stderr, "Valid commands: install, uninstall, start, stop\n")
		os.Exit(1)
	}
}

func installService() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get executable path: %v\n", err)
		os.Exit(1)
	}

	// Create config directory
	configDir := `C:\ProgramData\DNSServerMandiri`
	os.MkdirAll(configDir, 0755)
	os.MkdirAll(filepath.Join(configDir, "logs"), 0755)

	// Copy config if not exists
	configDst := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(configDst); os.IsNotExist(err) {
		// Try to copy from same directory as executable
		configSrc := filepath.Join(filepath.Dir(exePath), "config.yaml")
		if data, err := os.ReadFile(configSrc); err == nil {
			os.WriteFile(configDst, data, 0644)
			fmt.Printf("Config copied to: %s\n", configDst)
		} else {
			fmt.Printf("WARNING: No config.yaml found. Create one at: %s\n", configDst)
		}
	}

	// Open service manager
	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to service manager: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure to run as Administrator!\n")
		os.Exit(1)
	}
	defer m.Disconnect()

	// Check if service already exists
	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		fmt.Fprintf(os.Stderr, "Service '%s' already exists. Uninstall first.\n", serviceName)
		os.Exit(1)
	}

	// Create service
	svcConfig := mgr.Config{
		DisplayName:  serviceDisplayName,
		Description:  serviceDescription,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}

	s, err = m.CreateService(serviceName, exePath, svcConfig,
		"-config", configDst,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create service: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	// Setup event log
	err = eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		fmt.Printf("WARNING: Failed to setup event log: %v\n", err)
	}

	// Set recovery options (restart on failure)
	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	err = s.SetRecoveryActions(recoveryActions, 86400) // reset after 24h
	if err != nil {
		fmt.Printf("WARNING: Failed to set recovery actions: %v\n", err)
	}

	fmt.Println("=== Service Installed Successfully ===")
	fmt.Println("")
	fmt.Printf("  Service Name: %s\n", serviceName)
	fmt.Printf("  Executable:   %s\n", exePath)
	fmt.Printf("  Config:       %s\n", configDst)
	fmt.Printf("  Logs:         %s\\logs\\\n", configDir)
	fmt.Println("")
	fmt.Println("  Start with:   dns-server-windows.exe -service start")
	fmt.Println("  Or:           sc start DNSServerMandiri")
	fmt.Println("  Or:           net start DNSServerMandiri")
	fmt.Println("")
	fmt.Println("  Dashboard:    http://localhost:9153")
}

func uninstallService() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to service manager: %v\n", err)
		os.Exit(1)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Service '%s' not found: %v\n", serviceName, err)
		os.Exit(1)
	}
	defer s.Close()

	// Stop service if running
	status, err := s.Query()
	if err == nil && status.State == svc.Running {
		fmt.Println("Stopping service...")
		s.Control(svc.Stop)
		time.Sleep(3 * time.Second)
	}

	err = s.Delete()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to delete service: %v\n", err)
		os.Exit(1)
	}

	eventlog.Remove(serviceName)

	fmt.Println("Service uninstalled successfully.")
	fmt.Println("Config files remain at: C:\\ProgramData\\DNSServerMandiri\\")
}

func startService() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to service manager: %v\n", err)
		os.Exit(1)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Service '%s' not found: %v\n", serviceName, err)
		os.Exit(1)
	}
	defer s.Close()

	err = s.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start service: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Service started.")
	fmt.Println("Dashboard: http://localhost:9153")
}

func stopService() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to service manager: %v\n", err)
		os.Exit(1)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Service '%s' not found: %v\n", serviceName, err)
		os.Exit(1)
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stop service: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Service stop signal sent.")
}
