#Requires -RunAsAdministrator
# DNS Server Mandiri - Windows Uninstaller

$ErrorActionPreference = "Stop"

$ServiceName = "DNSServerMandiri"
$InstallDir = "C:\Program Files\DNSServerMandiri"

Write-Host "=== DNS Server Mandiri - Uninstaller ===" -ForegroundColor Cyan
Write-Host ""

# Stop and remove service
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.Status -eq "Running") {
        Write-Host "Stopping service..." -ForegroundColor Yellow
        Stop-Service -Name $ServiceName -Force
        Start-Sleep -Seconds 3
    }
    
    Write-Host "Removing service..." -ForegroundColor Yellow
    & "$InstallDir\dns-server-windows.exe" -service uninstall 2>$null
    if ($LASTEXITCODE -ne 0) {
        sc.exe delete $ServiceName | Out-Null
    }
    Write-Host "  Service removed." -ForegroundColor Green
} else {
    Write-Host "Service not found, skipping." -ForegroundColor Gray
}

# Remove firewall rules
Write-Host "Removing firewall rules..." -ForegroundColor Yellow
Remove-NetFirewallRule -DisplayName "DNS Server Mandiri (UDP)" -ErrorAction SilentlyContinue
Remove-NetFirewallRule -DisplayName "DNS Server Mandiri (TCP)" -ErrorAction SilentlyContinue
Remove-NetFirewallRule -DisplayName "DNS Server Mandiri (Dashboard)" -ErrorAction SilentlyContinue
Write-Host "  Firewall rules removed." -ForegroundColor Green

# Remove binary
Write-Host "Removing binary..." -ForegroundColor Yellow
if (Test-Path $InstallDir) {
    Remove-Item -Path $InstallDir -Recurse -Force
    Write-Host "  $InstallDir removed." -ForegroundColor Green
}

# Re-enable DNS Client if needed
Write-Host ""
$enableDns = Read-Host "Re-enable Windows DNS Client (Dnscache)? [y/N]"
if ($enableDns -eq "y" -or $enableDns -eq "Y") {
    Set-Service -Name "Dnscache" -StartupType Automatic
    Start-Service -Name "Dnscache"
    Write-Host "  Dnscache re-enabled." -ForegroundColor Green
}

Write-Host ""
Write-Host "=== Uninstall Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "  NOTE: Config and logs remain at: C:\ProgramData\DNSServerMandiri\" -ForegroundColor Yellow
Write-Host "  To remove completely: Remove-Item 'C:\ProgramData\DNSServerMandiri' -Recurse" -ForegroundColor Gray
