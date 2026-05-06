#!/bin/bash
# Configure existing AdGuard Home to use DNS Server Mandiri as upstream
# Usage: sudo bash configure-adguard.sh

set -e

echo "=========================================="
echo "Configure AdGuard Home for DNS Server Mandiri"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "Error: Please run as root (sudo)"
    exit 1
fi

# Check if AdGuard Home is installed
if [ ! -f "/opt/AdGuardHome/AdGuardHome" ]; then
    echo "Error: AdGuard Home is not installed"
    echo "Install it first: curl -s -S -L https://raw.githubusercontent.com/AdguardTeam/AdGuardHome/master/scripts/install.sh | sh -s -- -v"
    exit 1
fi

# Check if DNS Server Mandiri is running
if ! systemctl is-active --quiet dns-server; then
    echo "Error: DNS Server Mandiri is not running"
    echo "Start it first: sudo systemctl start dns-server"
    exit 1
fi

# Verify DNS Server Mandiri is listening on port 5353
if ! netstat -tulpn 2>/dev/null | grep -q ":5353.*dns-server" && ! ss -tulpn 2>/dev/null | grep -q ":5353.*dns-server"; then
    echo "Warning: DNS Server Mandiri may not be listening on port 5353"
    echo "Check with: sudo netstat -tulpn | grep 5353"
fi

echo "[1/3] Stopping AdGuard Home..."
systemctl stop AdGuardHome 2>/dev/null || true
sleep 2

echo "[2/3] Configuring AdGuard Home..."

# Backup existing config
if [ -f "/opt/AdGuardHome/AdGuardHome.yaml" ]; then
    echo "Backing up existing config..."
    cp /opt/AdGuardHome/AdGuardHome.yaml /opt/AdGuardHome/AdGuardHome.yaml.backup.$(date +%Y%m%d_%H%M%S)
fi

# Check if config exists
if [ -f "/opt/AdGuardHome/AdGuardHome.yaml" ]; then
    echo "Updating existing AdGuard Home config..."
    
    # Update upstream DNS to point to DNS Server Mandiri
    # This is a simple sed replacement - for complex configs, manual edit may be needed
    
    # Check if upstream_dns section exists
    if grep -q "upstream_dns:" /opt/AdGuardHome/AdGuardHome.yaml; then
        echo "  - Setting upstream DNS to 127.0.0.1:5353"
        
        # Create a temporary Python script to update YAML properly
        cat > /tmp/update_adguard.py <<'PYTHON'
import yaml
import sys

config_file = '/opt/AdGuardHome/AdGuardHome.yaml'

try:
    with open(config_file, 'r') as f:
        config = yaml.safe_load(f)
    
    # Update upstream DNS
    if 'dns' not in config:
        config['dns'] = {}
    
    config['dns']['upstream_dns'] = ['127.0.0.1:5353']
    config['dns']['bootstrap_dns'] = ['1.1.1.1', '8.8.8.8']
    
    # Optimize cache (DNS Server Mandiri has large cache)
    config['dns']['cache_size'] = 100000
    config['dns']['cache_optimistic'] = True
    
    # Enable EDNS
    if 'edns_client_subnet' not in config['dns']:
        config['dns']['edns_client_subnet'] = {}
    config['dns']['edns_client_subnet']['enabled'] = True
    
    with open(config_file, 'w') as f:
        yaml.dump(config, f, default_flow_style=False, sort_keys=False)
    
    print("✓ Config updated successfully")
    sys.exit(0)
    
except Exception as e:
    print(f"✗ Failed to update config: {e}")
    print("Please update manually:")
    print("  1. Edit /opt/AdGuardHome/AdGuardHome.yaml")
    print("  2. Set upstream_dns to: 127.0.0.1:5353")
    print("  3. Set bootstrap_dns to: 1.1.1.1, 8.8.8.8")
    sys.exit(1)
PYTHON
        
        # Try to update with Python if available
        if command -v python3 &> /dev/null; then
            # Check if PyYAML is installed
            if python3 -c "import yaml" 2>/dev/null; then
                python3 /tmp/update_adguard.py
            else
                echo "  PyYAML not installed, using sed (less reliable)..."
                # Fallback to sed - this is fragile but works for simple cases
                sed -i '/upstream_dns:/,/^[^ ]/ { /upstream_dns:/!{ /^[^ ]/!d; }; }' /opt/AdGuardHome/AdGuardHome.yaml
                sed -i '/upstream_dns:/a\    - 127.0.0.1:5353' /opt/AdGuardHome/AdGuardHome.yaml
                echo "  ⚠ Config updated with sed - please verify manually"
            fi
        else
            echo "  Python3 not found, manual configuration required"
            echo ""
            echo "  Please edit /opt/AdGuardHome/AdGuardHome.yaml manually:"
            echo "  Find the 'dns:' section and set:"
            echo "    upstream_dns:"
            echo "      - 127.0.0.1:5353"
            echo "    bootstrap_dns:"
            echo "      - 1.1.1.1"
            echo "      - 8.8.8.8"
        fi
        
        rm -f /tmp/update_adguard.py
    else
        echo "  ⚠ upstream_dns not found in config"
        echo "  Please configure via web UI after starting AdGuard Home"
    fi
else
    echo "No existing config found - will be created on first run"
    echo "Configure via web UI: http://$(hostname -I | awk '{print $1}'):3000"
fi

echo "[3/3] Starting services..."

# Start DNS Server Mandiri first
systemctl restart dns-server
sleep 2

# Start AdGuard Home
systemctl restart AdGuardHome
sleep 3

echo ""
echo "=========================================="
echo "Configuration Complete!"
echo "=========================================="
echo ""

# Check services
echo "Service Status:"
echo "---------------"

if systemctl is-active --quiet dns-server; then
    echo "✓ DNS Server Mandiri: RUNNING (port 5353)"
else
    echo "✗ DNS Server Mandiri: FAILED"
    echo "  Check logs: sudo journalctl -u dns-server -n 50"
fi

if systemctl is-active --quiet AdGuardHome; then
    echo "✓ AdGuard Home: RUNNING (port 53)"
else
    echo "✗ AdGuard Home: FAILED"
    echo "  Check logs: sudo journalctl -u AdGuardHome -n 50"
fi

echo ""

# Test DNS resolution
echo "Testing DNS Resolution:"
echo "-----------------------"

echo -n "DNS Server Mandiri (port 5353): "
if timeout 5 dig @127.0.0.1 -p 5353 google.com +short > /dev/null 2>&1; then
    echo "✓ OK"
else
    echo "✗ FAILED"
fi

echo -n "AdGuard Home (port 53): "
if timeout 5 dig @127.0.0.1 google.com +short > /dev/null 2>&1; then
    echo "✓ OK"
else
    echo "✗ FAILED"
fi

echo ""
echo "Next Steps:"
echo "-----------"
echo "1. Open AdGuard Home web UI:"
echo "   http://$(hostname -I | awk '{print $1}'):3000"
echo ""
echo "2. Verify Settings → DNS Settings:"
echo "   - Upstream DNS servers: 127.0.0.1:5353"
echo "   - Bootstrap DNS: 1.1.1.1, 8.8.8.8"
echo ""
echo "3. Configure your router/DHCP to use this server:"
echo "   DNS Server: $(hostname -I | awk '{print $1}')"
echo ""
echo "4. Monitor dashboards:"
echo "   - AdGuard Home: http://$(hostname -I | awk '{print $1}'):3000"
echo "   - DNS Server Mandiri: http://$(hostname -I | awk '{print $1}'):9153"
echo ""
