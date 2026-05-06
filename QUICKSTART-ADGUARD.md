# Quick Start: DNS Server Mandiri + AdGuard Home

Setup DNS Server Mandiri sebagai recursive resolver backend dengan AdGuard Home sebagai frontend UI (seperti Unbound + AdGuard).

## Instalasi Cepat (Linux)

```bash
# 1. Clone atau download project ini
cd dns-server-mandiri

# 2. Jalankan installer otomatis
sudo bash scripts/install-with-adguard.sh

# 3. Buka web browser
http://your-server-ip:3000

# 4. Selesaikan setup wizard AdGuard Home
# 5. Selesai! DNS server siap digunakan
```

## Arsitektur

```
Clients (port 53)
    ↓
AdGuard Home (filtering + UI)
    ↓
DNS Server Mandiri (port 5353 - recursive resolver)
    ↓
Internet (Root DNS Servers)
```

## File Penting

- **`config-upstream.yaml`** - Config DNS Server Mandiri untuk mode upstream
- **`adguard-config-template.yaml`** - Template config AdGuard Home
- **`scripts/install-with-adguard.sh`** - Installer otomatis
- **`ADGUARD-INTEGRATION.md`** - Dokumentasi lengkap

## Instalasi Manual

### 1. Install DNS Server Mandiri

```bash
# Build
make build

# Install
sudo cp bin/dns-server /usr/local/bin/
sudo mkdir -p /etc/dns-server /var/lib/dns-server
sudo cp config-upstream.yaml /etc/dns-server/config.yaml

# Create systemd service
sudo cp dns-server.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable dns-server
sudo systemctl start dns-server

# Verify
sudo systemctl status dns-server
dig @127.0.0.1 -p 5353 google.com
```

### 2. Install AdGuard Home

```bash
# Download dan install
curl -s -S -L https://raw.githubusercontent.com/AdguardTeam/AdGuardHome/master/scripts/install.sh | sh -s -- -v

# Buka web UI
http://your-server-ip:3000
```

### 3. Konfigurasi AdGuard Home

Di web UI, set **Upstream DNS** ke:
```
127.0.0.1:5353
```

**Bootstrap DNS:**
```
1.1.1.1
8.8.8.8
```

## Verifikasi

```bash
# Test DNS resolution
dig @localhost google.com

# Check services
sudo systemctl status dns-server
sudo systemctl status AdGuardHome

# View logs
sudo journalctl -u dns-server -f
sudo journalctl -u AdGuardHome -f
```

## Dashboard

- **AdGuard Home UI**: `http://your-server-ip:3000` (atau port 80)
- **DNS Server Mandiri**: `http://your-server-ip:9153`

## Konfigurasi Router/DHCP

Set DNS server di router/DHCP ke IP server ini:
```
Primary DNS: <server-ip>
Secondary DNS: 1.1.1.1 (backup)
```

## Troubleshooting

**AdGuard sudah terinstall sebelumnya:**
```bash
# Opsi 1: Konfigurasi AdGuard yang ada
sudo bash scripts/configure-adguard.sh

# Opsi 2: Reset dan reinstall fresh
sudo bash scripts/reinstall-adguard.sh
```

**Lupa password AdGuard:**
```bash
# Reset AdGuard (akan muncul setup wizard lagi)
sudo systemctl stop AdGuardHome
sudo rm /opt/AdGuardHome/AdGuardHome.yaml
sudo rm -rf /opt/AdGuardHome/data
sudo systemctl start AdGuardHome
# Buka http://your-server-ip:3000
```

**AdGuard tidak bisa resolve:**
```bash
# Cek DNS Server Mandiri running
sudo systemctl status dns-server
dig @127.0.0.1 -p 5353 google.com

# Konfigurasi ulang AdGuard
sudo bash scripts/configure-adguard.sh
```

**Port 53 sudah dipakai:**
```bash
# Cek service yang pakai port 53
sudo netstat -tulpn | grep :53

# Stop systemd-resolved jika ada
sudo systemctl stop systemd-resolved
sudo systemctl disable systemd-resolved
```

## Uninstall

```bash
sudo systemctl stop dns-server AdGuardHome
sudo systemctl disable dns-server AdGuardHome
sudo rm /usr/local/bin/dns-server
sudo rm -rf /etc/dns-server /var/lib/dns-server /opt/AdGuardHome
sudo rm /etc/systemd/system/dns-server.service
sudo systemctl daemon-reload
```

## Dokumentasi Lengkap

Lihat **`ADGUARD-INTEGRATION.md`** untuk:
- Konfigurasi detail
- Optimasi performa
- Monitoring & maintenance
- Backup & recovery
- FAQ

## Support

- GitHub Issues: [Link to your repo]
- Dashboard DNS Server: http://localhost:9153
- AdGuard Docs: https://github.com/AdguardTeam/AdGuardHome/wiki
