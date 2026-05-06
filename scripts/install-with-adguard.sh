#!/bin/bash
# Auto-install script for DNS Server Mandiri + AdGuard Home
# Usage: sudo bash install-with-adguard.sh

set -e

echo "=========================================="
echo "DNS Server Mandiri + AdGuard Home Installer"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "Error: Please run as root (sudo)"
    exit 1
fi

# Detect OS
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
else
    echo "Error: Cannot detect OS"
    exit 1
fi

echo "Detected OS: $OS"
echo ""

# ==================== Install DNS Server Mandiri ====================

echo "[1/5] Installing DNS Server Mandiri..."

# Build binary
if [ ! -f "bin/dns-server" ]; then
    echo "Building DNS Server Mandiri..."
    make build
fi

# Install binary
echo "Installing binary to /usr/local/bin/..."
cp bin/dns-server /usr/local/bin/
chmod +x /usr/local/bin/dns-server

# Create directories
echo "Creating directories..."
mkdir -p /etc/dns-server
mkdir -p /var/lib/dns-server

# Copy config (upstream mode)
echo "Installing configuration..."
if [ -f "config-upstream.yaml" ]; then
    cp config-upstream.yaml /etc/dns-server/config.yaml
else
    echo "Warning: config-upstream.yaml not found, using default config.yaml"
    cp config.yaml /etc/dns-server/config.yaml
    # Modify config to upstream mode
    sed -i 's/udp_port: 53/udp_port: 5353/' /etc/dns-server/config.yaml
    sed -i 's/tcp_port: 53/tcp_port: 5353/' /etc/dns-server/config.yaml
    sed -i 's/listen_addr: "0.0.0.0"/listen_addr: "127.0.0.1"/' /etc/dns-server/config.yaml
fi

# Create user
echo "Creating dns-server user..."
useradd -r -s /bin/false dns-server 2>/dev/null || true
chown -R dns-server:dns-server /var/lib/dns-server

# Install systemd service
echo "Installing systemd service..."
cat > /etc/systemd/system/dns-server.service <<'EOF'
[Unit]
Description=DNS Server Mandiri (Recursive Resolver)
After=network.target
Before=AdGuardHome.service

[Service]
Type=simple
User=dns-server
Group=dns-server
ExecStart=/usr/local/bin/dns-server -config /etc/dns-server/config.yaml
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/dns-server
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF

# Reload systemd
systemctl daemon-reload

# Start and enable service
echo "Starting DNS Server Mandiri..."
systemctl enable dns-server
systemctl start dns-server

# Wait for service to start
sleep 2

# Check if running
if systemctl is-active --quiet dns-server; then
    echo "✓ DNS Server Mandiri is running on port 5353"
else
    echo "✗ Failed to start DNS Server Mandiri"
    journalctl -u dns-server -n 20
    exit 1
fi

echo ""

# ==================== Install AdGuard Home ====================

echo "[2/5] Installing AdGuard Home..."

# Check if AdGuard already installed
if [ -f "/opt/AdGuardHome/AdGuardHome" ]; then
    echo "AdGuard Home already installed"
    echo "Stopping AdGuard Home for reconfiguration..."
    systemctl stop AdGuardHome 2>/dev/null || true
else
    # Download and install
    echo "Downloading AdGuard Home..."
    curl -s -S -L https://raw.githubusercontent.com/AdguardTeam/AdGuardHome/master/scripts/install.sh | sh -s -- -v
    
    # Wait for installation
    sleep 2
fi

echo ""

# ==================== Configure AdGuard Home ====================

echo "[3/5] Configuring AdGuard Home..."

# Stop AdGuard if running
systemctl stop AdGuardHome 2>/dev/null || true

# Create config directory
mkdir -p /opt/AdGuardHome

# Create AdGuard config
cat > /opt/AdGuardHome/AdGuardHome.yaml <<'EOF'
bind_host: 0.0.0.0
bind_port: 3000
users:
  - name: admin
    password: $2a$10$qwerty1234567890abcdefghijklmnopqrstuvwxyz  # Change via web UI
auth_attempts: 5
block_auth_min: 15
http_proxy: ""
language: ""
theme: auto
dns:
  bind_hosts:
    - 0.0.0.0
  port: 53
  anonymize_client_ip: false
  ratelimit: 0
  ratelimit_subnet_len_ipv4: 24
  ratelimit_subnet_len_ipv6: 56
  ratelimit_whitelist: []
  refuse_any: true
  upstream_dns:
    - 127.0.0.1:5353
  upstream_dns_file: ""
  bootstrap_dns:
    - 1.1.1.1
    - 8.8.8.8
  fallback_dns: []
  all_servers: false
  fastest_addr: false
  fastest_timeout: 1s
  allowed_clients: []
  disallowed_clients: []
  blocked_hosts:
    - version.bind
    - id.server
    - hostname.bind
  trusted_proxies:
    - 127.0.0.0/8
    - ::1/128
  cache_size: 100000
  cache_ttl_min: 0
  cache_ttl_max: 0
  cache_optimistic: true
  bogus_nxdomain: []
  aaaa_disabled: false
  enable_dnssec: false
  edns_client_subnet:
    custom_ip: ""
    enabled: true
    use_custom: false
  max_goroutines: 300
  handle_ddr: true
  ipset: []
  ipset_file: ""
  bootstrap_prefer_ipv6: false
  upstream_timeout: 10s
  private_networks: []
  use_private_ptr_resolvers: true
  local_ptr_upstreams: []
  use_dns64: false
  dns64_prefixes: []
  serve_http3: false
  use_http3_upstreams: false
tls:
  enabled: false
  server_name: ""
  force_https: false
  port_https: 443
  port_dns_over_tls: 853
  port_dns_over_quic: 853
  port_dnscrypt: 0
  dnscrypt_config_file: ""
  allow_unencrypted_doh: false
  certificate_chain: ""
  private_key: ""
  certificate_path: ""
  private_key_path: ""
  strict_sni_check: false
querylog:
  ignored: []
  interval: 2160h
  size_memory: 1000
  enabled: true
  file_enabled: true
statistics:
  ignored: []
  interval: 24h
  enabled: true
filters:
  - enabled: true
    url: https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt
    name: AdGuard DNS filter
    id: 1
  - enabled: true
    url: https://raw.githubusercontent.com/hagezi/dns-blocklists/main/wildcard/pro-onlydomains.txt
    name: HaGeZi Pro
    id: 2
whitelist_filters: []
user_rules: []
dhcp:
  enabled: false
  interface_name: ""
  local_domain_name: lan
  dhcpv4:
    gateway_ip: ""
    subnet_mask: ""
    range_start: ""
    range_end: ""
    lease_duration: 86400
    icmp_timeout_msec: 1000
    options: []
  dhcpv6:
    range_start: ""
    lease_duration: 86400
    ra_slaac_only: false
    ra_allow_slaac: false
filtering:
  blocking_ipv4: ""
  blocking_ipv6: ""
  blocked_services:
    schedule:
      time_zone: UTC
    ids: []
  protection_disabled_until: null
  safe_search:
    enabled: false
    bing: true
    duckduckgo: true
    google: true
    pixabay: true
    yandex: true
    youtube: true
  blocking_mode: default
  parental_block_host: family-block.dns.adguard.com
  safebrowsing_block_host: standard-block.dns.adguard.com
  rewrites: []
  safebrowsing_cache_size: 1048576
  safesearch_cache_size: 1048576
  parental_cache_size: 1048576
  cache_time: 30
  filters_update_interval: 24
  blocked_response_ttl: 10
  filtering_enabled: true
  parental_enabled: false
  safebrowsing_enabled: false
  protection_enabled: true
clients:
  runtime_sources:
    whois: true
    arp: true
    rdns: true
    dhcp: true
    hosts: true
  persistent: []
log:
  file: ""
  max_backups: 0
  max_size: 100
  max_age: 3
  compress: false
  local_time: false
  verbose: false
os:
  group: ""
  user: ""
  rlimit_nofile: 0
schema_version: 28
EOF

echo "✓ AdGuard Home configured"
echo ""

# ==================== Start Services ====================

echo "[4/5] Starting services..."

# Start DNS Server Mandiri first
systemctl restart dns-server
sleep 2

# Start AdGuard Home
systemctl enable AdGuardHome
systemctl restart AdGuardHome
sleep 3

# Check services
echo ""
echo "Service Status:"
echo "---------------"

if systemctl is-active --quiet dns-server; then
    echo "✓ DNS Server Mandiri: RUNNING (port 5353)"
else
    echo "✗ DNS Server Mandiri: FAILED"
fi

if systemctl is-active --quiet AdGuardHome; then
    echo "✓ AdGuard Home: RUNNING (port 53)"
else
    echo "✗ AdGuard Home: FAILED"
fi

echo ""

# ==================== Verification ====================

echo "[5/5] Verifying installation..."

# Test DNS Server Mandiri
echo -n "Testing DNS Server Mandiri (port 5353)... "
if timeout 5 dig @127.0.0.1 -p 5353 google.com +short > /dev/null 2>&1; then
    echo "✓ OK"
else
    echo "✗ FAILED"
fi

# Test AdGuard Home
echo -n "Testing AdGuard Home (port 53)... "
if timeout 5 dig @127.0.0.1 google.com +short > /dev/null 2>&1; then
    echo "✓ OK"
else
    echo "✗ FAILED"
fi

echo ""

# ==================== Final Instructions ====================

echo "=========================================="
echo "Installation Complete!"
echo "=========================================="
echo ""
echo "Next Steps:"
echo ""
echo "1. Open AdGuard Home web interface:"
echo "   http://$(hostname -I | awk '{print $1}'):3000"
echo ""
echo "2. Complete the setup wizard:"
echo "   - Set admin username and password"
echo "   - Configure web interface port (default: 3000 or 80)"
echo "   - DNS server port is already set to 53"
echo ""
echo "3. Verify upstream DNS is set to 127.0.0.1:5353"
echo "   (Should be already configured)"
echo ""
echo "4. Configure your router/DHCP to use this server as DNS:"
echo "   DNS Server: $(hostname -I | awk '{print $1}')"
echo ""
echo "5. Monitor dashboards:"
echo "   - AdGuard Home: http://$(hostname -I | awk '{print $1}'):3000"
echo "   - DNS Server Mandiri: http://$(hostname -I | awk '{print $1}'):9153"
echo ""
echo "Logs:"
echo "  sudo journalctl -u dns-server -f"
echo "  sudo journalctl -u AdGuardHome -f"
echo ""
echo "Uninstall:"
echo "  sudo systemctl stop dns-server AdGuardHome"
echo "  sudo systemctl disable dns-server AdGuardHome"
echo "  sudo rm /usr/local/bin/dns-server"
echo "  sudo rm -rf /etc/dns-server /var/lib/dns-server /opt/AdGuardHome"
echo ""
echo "=========================================="
