#!/bin/bash
# Uninstall AdGuard Home and restore DNS Server Mandiri to standalone mode
# Usage: sudo bash uninstall-adguard.sh

set -e

echo "=========================================="
echo "Remove AdGuard & Restore DNS Server Mandiri"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "Error: Please run as root (sudo)"
    exit 1
fi

echo "This will:"
echo "  1. Stop and uninstall AdGuard Home"
echo "  2. Restore DNS Server Mandiri to standalone mode (port 53)"
echo "  3. Remove AdGuard data (optional backup)"
echo ""
read -p "Continue? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
fi

echo ""

# ==================== Backup AdGuard (Optional) ====================

read -p "Backup AdGuard data before removing? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    if [ -d "/opt/AdGuardHome" ]; then
        BACKUP_DIR="/opt/AdGuardHome.backup.$(date +%Y%m%d_%H%M%S)"
        echo "[1/5] Backing up AdGuard to: $BACKUP_DIR"
        cp -r /opt/AdGuardHome "$BACKUP_DIR"
        echo "  ✓ Backup complete"
    fi
else
    echo "[1/5] Skipping backup..."
fi

echo ""

# ==================== Stop Services ====================

echo "[2/5] Stopping services..."

# Stop AdGuard
if systemctl is-active --quiet AdGuardHome; then
    systemctl stop AdGuardHome
    echo "  ✓ AdGuard Home stopped"
fi

# Stop DNS Server Mandiri
if systemctl is-active --quiet dns-server; then
    systemctl stop dns-server
    echo "  ✓ DNS Server Mandiri stopped"
fi

echo ""

# ==================== Uninstall AdGuard ====================

echo "[3/5] Uninstalling AdGuard Home..."

# Disable and remove service
systemctl disable AdGuardHome 2>/dev/null || true
rm -f /etc/systemd/system/AdGuardHome.service

# Remove AdGuard files
rm -rf /opt/AdGuardHome
rm -f /usr/local/bin/AdGuardHome

# Reload systemd
systemctl daemon-reload

echo "  ✓ AdGuard Home removed"
echo ""

# ==================== Restore DNS Server Config ====================

echo "[4/5] Restoring DNS Server Mandiri to standalone mode..."

# Check if original config exists
if [ -f "config.yaml" ]; then
    echo "  Using config.yaml from repository..."
    cp config.yaml /etc/dns-server/config.yaml
elif [ -f "/etc/dns-server/config.yaml" ]; then
    echo "  Modifying existing config..."
    # Change port back to 53
    sed -i 's/udp_port: 5353/udp_port: 53/' /etc/dns-server/config.yaml
    sed -i 's/tcp_port: 5353/tcp_port: 53/' /etc/dns-server/config.yaml
    # Change listen address to 0.0.0.0
    sed -i 's/listen_addr: "127.0.0.1"/listen_addr: "0.0.0.0"/' /etc/dns-server/config.yaml
    # Enable filtering
    sed -i 's/enabled: false/enabled: true/' /etc/dns-server/config.yaml
else
    echo "  Error: No config file found!"
    exit 1
fi

echo "  ✓ Config restored to standalone mode"
echo ""

# ==================== Start DNS Server ====================

echo "[5/5] Starting DNS Server Mandiri..."

systemctl enable dns-server
systemctl start dns-server

# Wait for service to start
sleep 2

# Check if running
if systemctl is-active --quiet dns-server; then
    echo "  ✓ DNS Server Mandiri is running on port 53"
else
    echo "  ✗ Failed to start DNS Server Mandiri"
    journalctl -u dns-server -n 20
    exit 1
fi

echo ""

# ==================== Verification ====================

echo "Verifying DNS resolution..."
sleep 1

if timeout 5 dig @127.0.0.1 google.com +short > /dev/null 2>&1; then
    echo "  ✓ DNS resolution working"
else
    echo "  ✗ DNS resolution failed"
    echo "  Check logs: sudo journalctl -u dns-server -n 50"
fi

echo ""

# ==================== Summary ====================

echo "=========================================="
echo "Restoration Complete!"
echo "=========================================="
echo ""
echo "DNS Server Mandiri Status:"
echo "  - Running on: 0.0.0.0:53 (UDP & TCP)"
echo "  - Dashboard: http://$(hostname -I | awk '{print $1}'):9153"
echo "  - Mode: Standalone (with built-in filtering)"
echo ""
echo "AdGuard Home:"
echo "  - Status: REMOVED"
if [ -n "$BACKUP_DIR" ]; then
    echo "  - Backup: $BACKUP_DIR"
fi
echo ""
echo "Next Steps:"
echo "  1. Configure router/DHCP to use this server:"
echo "     DNS Server: $(hostname -I | awk '{print $1}')"
echo ""
echo "  2. Monitor dashboard:"
echo "     http://$(hostname -I | awk '{print $1}'):9153"
echo ""
echo "  3. Check logs:"
echo "     sudo journalctl -u dns-server -f"
echo ""
echo "  4. Test DNS from client:"
echo "     dig @$(hostname -I | awk '{print $1}') google.com"
echo ""
echo "Config file: /etc/dns-server/config.yaml"
echo ""
