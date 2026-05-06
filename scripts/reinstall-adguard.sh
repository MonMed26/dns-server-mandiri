#!/bin/bash
# Uninstall AdGuard Home completely and reinstall fresh
# Usage: sudo bash reinstall-adguard.sh

set -e

echo "=========================================="
echo "AdGuard Home - Complete Reinstall"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "Error: Please run as root (sudo)"
    exit 1
fi

echo "This will:"
echo "  1. Stop and uninstall AdGuard Home"
echo "  2. Remove all data and configuration"
echo "  3. Reinstall AdGuard Home fresh"
echo "  4. Configure it to use DNS Server Mandiri"
echo ""
read -p "Continue? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
fi

echo ""
echo "[1/4] Stopping AdGuard Home..."
systemctl stop AdGuardHome 2>/dev/null || true
systemctl disable AdGuardHome 2>/dev/null || true

echo "[2/4] Backing up old data..."
if [ -d "/opt/AdGuardHome" ]; then
    BACKUP_DIR="/opt/AdGuardHome.backup.$(date +%Y%m%d_%H%M%S)"
    echo "  Backing up to: $BACKUP_DIR"
    mv /opt/AdGuardHome "$BACKUP_DIR"
    echo "  ✓ Backup complete"
fi

echo "[3/4] Uninstalling AdGuard Home..."
# Remove systemd service
rm -f /etc/systemd/system/AdGuardHome.service
systemctl daemon-reload

# Remove binary if exists
rm -f /usr/local/bin/AdGuardHome

echo "[4/4] Installing AdGuard Home fresh..."
curl -s -S -L https://raw.githubusercontent.com/AdguardTeam/AdGuardHome/master/scripts/install.sh | sh -s -- -v

echo ""
echo "=========================================="
echo "Installation Complete!"
echo "=========================================="
echo ""
echo "Next Steps:"
echo ""
echo "1. Open AdGuard Home setup wizard:"
echo "   http://$(hostname -I | awk '{print $1}'):3000"
echo ""
echo "2. Follow the setup wizard:"
echo "   - Create admin username and password"
echo "   - Set web interface port (default: 3000 or 80)"
echo "   - DNS server port: 53 (default)"
echo ""
echo "3. After setup, configure upstream DNS:"
echo "   Go to Settings → DNS Settings"
echo "   Set Upstream DNS servers to: 127.0.0.1:5353"
echo "   Set Bootstrap DNS to: 1.1.1.1, 8.8.8.8"
echo ""
echo "4. Or run auto-configure script:"
echo "   sudo bash scripts/configure-adguard.sh"
echo ""
echo "Old data backed up to: $BACKUP_DIR"
echo ""
