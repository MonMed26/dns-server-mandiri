#Requires -RunAsAdministrator
# DNS Server Mandiri - Windows Installation Script
# Run as Administrator: powershell -ExecutionPolicy Bypass -File install.ps1

$ErrorActionPreference = "Stop"

$ServiceName = "DNSServerMandiri"
$InstallDir = "C:\Program Files\DNSServerMandiri"
$DataDir = "C:\ProgramData\DNSServerMandiri"
$LogDir = "$DataDir\logs"
$BinaryName = "dns-server-windows.exe"

Write-Host "=== DNS Server Mandiri - Windows Installer ===" -ForegroundColor Cyan
Write-Host ""

# Step 1: Check if port 53 is in use
Write-Host "[1/6] Checking port 53..." -ForegroundColor Yellow
$port53 = Get-NetTCPConnection -LocalPort 53 -ErrorAction SilentlyContinue
$port53udp = Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue

if ($port53 -or $port53udp) {
    Write-Host "  WARNING: Port 53 is already in use!" -ForegroundColor Red
    Write-Host "  Checking what's using it..."
    
    # Check for Windows DNS Client service
    $dnsClient = Get-Service -Name "Dnscache" -ErrorAction SilentlyContinue
    if ($dnsClient -and $dnsClient.Status -eq "Running") {
        Write-Host "  Windows DNS Client (Dnscache) is running." -ForegroundColor Yellow
        Write-Host "  Stopping and disabling Dnscache service..."
        Stop-Service -Name "Dnscache" -Force
        Set-Service -Name "Dnscache" -StartupType Disabled
        Write-Host "  Done. Dnscache disabled." -ForegroundColor Green
    }
    
    # Check again
    Start-Sleep -Seconds 2
    $port53 = Get-NetTCPConnection -LocalPort 53 -ErrorAction SilentlyContinue
    $port53udp = Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue
    if ($port53 -or $port53udp) {
        Write-Host "  ERROR: Port 53 still in use. Please free it manually." -ForegroundColor Red
        Write-Host "  Use: netstat -ano | findstr :53" -ForegroundColor Gray
        exit 1
    }
}
Write-Host "  Port 53 is available." -ForegroundColor Green

# Step 2: Create directories
Write-Host "[2/6] Creating directories..." -ForegroundColor Yellow
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
New-Item -ItemType Directory -Path $DataDir -Force | Out-Null
New-Item -ItemType Directory -Path $LogDir -Force | Out-Null
Write-Host "  $InstallDir" -ForegroundColor Gray
Write-Host "  $DataDir" -ForegroundColor Gray
Write-Host "  $LogDir" -ForegroundColor Gray

# Step 3: Build or copy binary
Write-Host "[3/6] Installing binary..." -ForegroundColor Yellow
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)

$binarySrc = Join-Path $projectRoot "bin\$BinaryName"
if (-not (Test-Path $binarySrc)) {
    Write-Host "  Building from source..." -ForegroundColor Gray
    Push-Location $projectRoot
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -ldflags "-s -w -X main.version=1.0.0" -o "bin\$BinaryName" ./cmd/dns-server-windows
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  ERROR: Build failed!" -ForegroundColor Red
        Pop-Location
        exit 1
    }
    Pop-Location
    Write-Host "  Build successful." -ForegroundColor Green
}

Copy-Item $binarySrc "$InstallDir\$BinaryName" -Force
Write-Host "  Binary installed: $InstallDir\$BinaryName" -ForegroundColor Green

# Step 4: Install config
Write-Host "[4/6] Installing configuration..." -ForegroundColor Yellow
$configDst = "$DataDir\config.yaml"
if (-not (Test-Path $configDst)) {
    $configSrc = Join-Path $projectRoot "config.yaml"
    if (Test-Path $configSrc) {
        Copy-Item $configSrc $configDst
        Write-Host "  Config installed: $configDst" -ForegroundColor Green
    } else {
        Write-Host "  WARNING: No config.yaml found. Using defaults." -ForegroundColor Yellow
    }
} else {
    Write-Host "  Config already exists, skipping." -ForegroundColor Gray
}

# Step 5: Install Windows Service
Write-Host "[5/6] Installing Windows Service..." -ForegroundColor Yellow
$existingService = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existingService) {
    Write-Host "  Service already exists. Removing old service..."
    if ($existingService.Status -eq "Running") {
        Stop-Service -Name $ServiceName -Force
        Start-Sleep -Seconds 2
    }
    sc.exe delete $ServiceName | Out-Null
    Start-Sleep -Seconds 2
}

# Install service using the binary's built-in service installer
& "$InstallDir\$BinaryName" -service install
if ($LASTEXITCODE -ne 0) {
    Write-Host "  ERROR: Service installation failed!" -ForegroundColor Red
    exit 1
}

# Step 6: Configure Windows Firewall
Write-Host "[6/6] Configuring firewall..." -ForegroundColor Yellow
$fwRuleUDP = Get-NetFirewallRule -DisplayName "DNS Server Mandiri (UDP)" -ErrorAction SilentlyContinue
if (-not $fwRuleUDP) {
    New-NetFirewallRule -DisplayName "DNS Server Mandiri (UDP)" `
        -Direction Inbound -Protocol UDP -LocalPort 53 `
        -Action Allow -Profile Any | Out-Null
    Write-Host "  Firewall rule added: UDP 53" -ForegroundColor Green
}

$fwRuleTCP = Get-NetFirewallRule -DisplayName "DNS Server Mandiri (TCP)" -ErrorAction SilentlyContinue
if (-not $fwRuleTCP) {
    New-NetFirewallRule -DisplayName "DNS Server Mandiri (TCP)" `
        -Direction Inbound -Protocol TCP -LocalPort 53 `
        -Action Allow -Profile Any | Out-Null
    Write-Host "  Firewall rule added: TCP 53" -ForegroundColor Green
}

$fwRuleDash = Get-NetFirewallRule -DisplayName "DNS Server Mandiri (Dashboard)" -ErrorAction SilentlyContinue
if (-not $fwRuleDash) {
    New-NetFirewallRule -DisplayName "DNS Server Mandiri (Dashboard)" `
        -Direction Inbound -Protocol TCP -LocalPort 9153 `
        -Action Allow -Profile Any | Out-Null
    Write-Host "  Firewall rule added: TCP 9153 (Dashboard)" -ForegroundColor Green
}

# Start the service
Write-Host ""
Write-Host "Starting service..." -ForegroundColor Yellow
Start-Service -Name $ServiceName
Start-Sleep -Seconds 3

$svc = Get-Service -Name $ServiceName
if ($svc.Status -eq "Running") {
    Write-Host ""
    Write-Host "=== Installation Complete! ===" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Status:    RUNNING" -ForegroundColor Green
    Write-Host "  DNS:       0.0.0.0:53 (UDP/TCP)" -ForegroundColor White
    Write-Host "  Dashboard: http://localhost:9153" -ForegroundColor Cyan
    Write-Host "  Config:    $configDst" -ForegroundColor Gray
    Write-Host "  Logs:      $LogDir" -ForegroundColor Gray
    Write-Host ""
    Write-Host "  Commands:" -ForegroundColor White
    Write-Host "    Start:   Start-Service $ServiceName" -ForegroundColor Gray
    Write-Host "    Stop:    Stop-Service $ServiceName" -ForegroundColor Gray
    Write-Host "    Status:  Get-Service $ServiceName" -ForegroundColor Gray
    Write-Host "    Logs:    Get-Content $LogDir\dns-server.log -Tail 50" -ForegroundColor Gray
    Write-Host ""
    Write-Host "  Test:" -ForegroundColor White
    Write-Host "    nslookup google.com 127.0.0.1" -ForegroundColor Gray
    Write-Host "    Resolve-DnsName -Name google.com -Server 127.0.0.1" -ForegroundColor Gray
    Write-Host ""
    Write-Host "  Mikrotik:" -ForegroundColor White
    Write-Host "    /ip dns set servers=<THIS_PC_IP>" -ForegroundColor Gray
} else {
    Write-Host ""
    Write-Host "  ERROR: Service failed to start!" -ForegroundColor Red
    Write-Host "  Check logs: Get-Content $LogDir\dns-server.log" -ForegroundColor Yellow
    Write-Host "  Event log:  Get-EventLog -LogName Application -Source $ServiceName -Newest 10" -ForegroundColor Yellow
    exit 1
}
