@echo off
REM DNS Server Mandiri - Development Runner (Console Mode)
REM Run this to test locally without installing as service

echo === DNS Server Mandiri (Development Mode) ===
echo.
echo DNS:       127.0.0.1:53
echo Dashboard: http://localhost:9153
echo.
echo Press Ctrl+C to stop
echo.

cd /d "%~dp0\..\.."
go run ./cmd/dns-server-windows -config config.yaml -log-level debug -query-log
