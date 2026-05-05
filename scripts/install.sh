#!/bin/bash
# DNS Server Mandiri - Installation Script
# Run on your Proxmox CT (Debian/Ubuntu)

set -e

echo "=== DNS Server Mandiri - Installer ==="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "ERROR: Please run as root (sudo)"
    exit 1
fi

# Detect OS
if [ -f /etc/debian_version ]; then
    OS="debian"
elif [ -f /etc/alpine-release ]; then
    OS="alpine"
else
    echo "WARNING: Unsupported OS, proceeding anyway..."
    OS="unknown"
fi

echo "[1/7] Stopping conflicting services..."
# Stop systemd-resolved if running (conflicts with port 53)
if systemctl is-active --quiet systemd-resolved 2>/dev/null; then
    systemctl stop systemd-resolved
    systemctl disable systemd-resolved
    # Fix /etc/resolv.conf
    rm -f /etc/resolv.conf
    echo "nameserver 8.8.8.8" > /etc/resolv.conf
    echo "  - systemd-resolved stopped and disabled"
fi

# Stop dnsmasq if running
if systemctl is-active --quiet dnsmasq 2>/dev/null; then
    systemctl stop dnsmasq
    systemctl disable dnsmasq
    echo "  - dnsmasq stopped and disabled"
fi

echo "[2/7] Creating dns-server user..."
useradd -r -s /bin/false dns-server 2>/dev/null || echo "  - User already exists"

echo "[3/7] Installing binary..."
if [ ! -f "./bin/dns-server" ]; then
    echo "  Building from source..."
    # Check if Go is installed
    if ! command -v go &> /dev/null; then
        echo "  Installing Go..."
        if [ "$OS" = "debian" ]; then
            apt-get update -qq
            apt-get install -y -qq golang-go
        elif [ "$OS" = "alpine" ]; then
            apk add --no-cache go
        fi
    fi
    CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/dns-server ./cmd/dns-server
fi

cp bin/dns-server /usr/local/bin/dns-server
chmod 755 /usr/local/bin/dns-server
echo "  - Binary installed to /usr/local/bin/dns-server"

echo "[4/7] Installing configuration..."
mkdir -p /etc/dns-server
if [ ! -f /etc/dns-server/config.yaml ]; then
    cp config.yaml /etc/dns-server/config.yaml
    echo "  - Config installed to /etc/dns-server/config.yaml"
else
    echo "  - Config already exists, skipping (backup: config.yaml.new)"
    cp config.yaml /etc/dns-server/config.yaml.new
fi

echo "[5/7] Setting up log directory..."
mkdir -p /var/log/dns-server
chown dns-server:dns-server /var/log/dns-server

echo "[6/7] Installing systemd service..."
cp dns-server.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable dns-server
echo "  - Service enabled"

echo "[7/7] Starting DNS server..."
systemctl start dns-server
sleep 2

if systemctl is-active --quiet dns-server; then
    echo ""
    echo "=== Installation Complete! ==="
    echo ""
    echo "Status: RUNNING"
    echo "  DNS:     0.0.0.0:53 (UDP/TCP)"
    echo "  Metrics: http://0.0.0.0:9153/metrics"
    echo "  Health:  http://0.0.0.0:9153/health"
    echo ""
    echo "Commands:"
    echo "  systemctl status dns-server    # Check status"
    echo "  systemctl restart dns-server   # Restart"
    echo "  journalctl -u dns-server -f    # View logs"
    echo ""
    echo "Test with:"
    echo "  dig @127.0.0.1 google.com"
    echo "  dig @127.0.0.1 tokopedia.com"
    echo ""
    echo "Mikrotik setup:"
    echo "  /ip dns set servers=<THIS_SERVER_IP>"
    echo "  /ip dns set allow-remote-requests=yes"
else
    echo ""
    echo "ERROR: Service failed to start!"
    echo "Check logs: journalctl -u dns-server -n 50"
    exit 1
fi
