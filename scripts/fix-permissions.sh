#!/bin/bash
# Fix DNS Server Mandiri permission issues
# Usage: sudo bash fix-permissions.sh

set -e

echo "=========================================="
echo "Fix DNS Server Mandiri Permissions"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "Error: Please run as root (sudo)"
    exit 1
fi

echo "[1/6] Stopping service..."
systemctl stop dns-server 2>/dev/null || true
sleep 2

echo "[2/6] Setting binary capabilities..."
setcap 'cap_net_bind_service=+ep' /usr/local/bin/dns-server
echo "  ✓ Capability set"
getcap /usr/local/bin/dns-server

echo ""
echo "[3/6] Fixing directory permissions..."
mkdir -p /var/lib/dns-server
chown -R dns-server:dns-server /var/lib/dns-server
chmod 755 /var/lib/dns-server
echo "  ✓ /var/lib/dns-server permissions fixed"

echo ""
echo "[4/6] Updating systemd service..."
cat > /etc/systemd/system/dns-server.service <<'EOF'
[Unit]
Description=DNS Server Mandiri (Recursive Resolver)
After=network.target

[Service]
Type=simple
User=dns-server
Group=dns-server
ExecStart=/usr/local/bin/dns-server -config /etc/dns-server/config.yaml
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

# Allow binding to port 53
AmbientCapabilities=CAP_NET_BIND_SERVICE

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/dns-server

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
echo "  ✓ Systemd service updated"

echo ""
echo "[5/6] Starting service..."
systemctl start dns-server
sleep 3

echo ""
echo "[6/6] Checking status..."
if systemctl is-active --quiet dns-server; then
    echo "  ✓ DNS Server is RUNNING"
    echo ""
    systemctl status dns-server --no-pager -l
else
    echo "  ✗ DNS Server FAILED to start"
    echo ""
    echo "Last 30 log lines:"
    journalctl -u dns-server -n 30 --no-pager
    exit 1
fi

echo ""
echo "=========================================="
echo "Testing DNS Resolution"
echo "=========================================="
echo ""

# Wait a bit for server to fully start
sleep 2

# Test DNS
echo -n "Testing DNS query... "
if timeout 5 dig @127.0.0.1 google.com +short > /dev/null 2>&1; then
    echo "✓ OK"
    dig @127.0.0.1 google.com +short | head -3
else
    echo "✗ FAILED"
    echo "Check logs: sudo journalctl -u dns-server -f"
fi

echo ""
echo "Dashboard: http://$(hostname -I | awk '{print $1}'):9153"
echo ""
