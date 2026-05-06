# AdGuard Home + DNS Server Mandiri Integration

Setup DNS Server Mandiri sebagai **recursive resolver backend** (seperti Unbound) dengan **AdGuard Home sebagai frontend UI**.

```
┌─────────┐      ┌──────────────┐      ┌────────────────────┐      ┌──────────┐
│ Clients │ ───> │ AdGuard Home │ ───> │ DNS Server Mandiri │ ───> │ Internet │
│ (port   │      │ (port 53)    │      │ (port 5353)        │      │ (Root    │
│  53)    │      │ UI + Filter  │      │ Recursive Resolver │      │  Servers)│
└─────────┘      └──────────────┘      └────────────────────┘      └──────────┘
                  - Filtering            - Cache 500K entries
                  - Statistics           - Full recursive
                  - Web UI               - EDNS support
                  - Blocklists           - High performance
```

## Keuntungan Setup Ini

✅ **UI AdGuard yang mature** - Dashboard, statistics, query log yang lengkap  
✅ **Filtering powerful** - Blocklists, parental control, safe browsing  
✅ **Recursive resolver custom** - Tidak bergantung pada upstream public DNS  
✅ **Cache massive** - 500K entries untuk performa optimal  
✅ **Privacy** - Semua query di-resolve sendiri, tidak ke pihak ketiga  
✅ **Monitoring dual** - AdGuard UI (port 80/3000) + DNS Server dashboard (port 9153)

---

## Instalasi

### 1. Install DNS Server Mandiri (Backend)

#### Linux

```bash
# Build dan install
cd dns-server-mandiri
make build
sudo make install

# Copy config untuk mode upstream
sudo cp config-upstream.yaml /etc/dns-server/config.yaml

# Create data directory
sudo mkdir -p /var/lib/dns-server

# Start service
sudo systemctl start dns-server
sudo systemctl enable dns-server

# Verify running on port 5353
sudo netstat -tulpn | grep 5353
```

#### Windows

```powershell
# Build
make build-windows

# Copy config
copy config-upstream.yaml config.yaml

# Install as Windows Service
.\bin\dns-server-windows.exe install

# Start service
.\bin\dns-server-windows.exe start

# Verify
netstat -an | findstr 5353
```

---

### 2. Install AdGuard Home (Frontend)

#### Linux

```bash
# Download dan install
curl -s -S -L https://raw.githubusercontent.com/AdguardTeam/AdGuardHome/master/scripts/install.sh | sh -s -- -v

# AdGuard akan berjalan di http://localhost:3000 untuk setup awal
```

#### Windows

```powershell
# Download dari https://github.com/AdguardTeam/AdGuardHome/releases
# Extract dan jalankan AdGuardHome.exe
# Buka http://localhost:3000 untuk setup
```

---

### 3. Konfigurasi AdGuard Home

#### Setup Wizard (Web UI)

1. Buka `http://your-server-ip:3000`
2. Ikuti wizard setup:
   - **Admin username/password**: Buat credentials
   - **Web interface port**: 80 atau 3000 (default)
   - **DNS server port**: 53

#### Konfigurasi Upstream DNS

Setelah setup wizard, masuk ke **Settings → DNS Settings**:

**Upstream DNS servers:**
```
127.0.0.1:5353
```

**Bootstrap DNS servers** (untuk resolve domain AdGuard sendiri):
```
1.1.1.1
8.8.8.8
```

**DNS cache configuration:**
```
Cache size: 100000
```
(Lebih kecil karena DNS Server Mandiri sudah punya cache 500K)

**Advanced settings:**
- ✅ Enable EDNS
- ✅ Enable DNSSEC (jika DNS Server Mandiri enable DNSSEC)
- ⬜ Disable parallel requests (tidak perlu, hanya 1 upstream)

#### Konfigurasi via File (Advanced)

Edit `/opt/AdGuardHome/AdGuardHome.yaml`:

```yaml
dns:
  bind_hosts:
    - 0.0.0.0
  port: 53
  
  # DNS Server Mandiri sebagai upstream
  upstream_dns:
    - 127.0.0.1:5353
  
  # Bootstrap untuk resolve domain AdGuard
  bootstrap_dns:
    - 1.1.1.1
    - 8.8.8.8
  
  # Cache kecil (DNS Server Mandiri sudah cache besar)
  cache_size: 100000
  cache_ttl_min: 0
  cache_ttl_max: 0  # Use upstream TTL
  cache_optimistic: true
  
  # EDNS
  edns_client_subnet:
    enabled: true
  
  # Rate limiting (opsional, DNS Server Mandiri sudah handle)
  ratelimit: 0
  
  # Filtering
  filtering_enabled: true
  filters_update_interval: 24

# Web interface
http:
  address: 0.0.0.0:80
  
# Statistics
statistics:
  enabled: true
  interval: 24h
  
# Query log
querylog:
  enabled: true
  interval: 2160h  # 90 days
  size_memory: 1000
  
# Filters (contoh)
filters:
  - enabled: true
    url: https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt
    name: AdGuard DNS filter
    id: 1
  - enabled: true
    url: https://raw.githubusercontent.com/hagezi/dns-blocklists/main/wildcard/pro-onlydomains.txt
    name: HaGeZi Pro
    id: 2
```

Restart AdGuard:
```bash
sudo systemctl restart AdGuardHome
```

---

## Verifikasi Setup

### 1. Test DNS Resolution

```bash
# Test via AdGuard (port 53)
dig @localhost google.com

# Test langsung ke DNS Server Mandiri (port 5353)
dig @localhost -p 5353 google.com

# Test dari client lain
dig @your-server-ip google.com
```

### 2. Test Filtering

```bash
# Test blocked domain (setelah enable blocklist di AdGuard)
dig @localhost doubleclick.net

# Harusnya return 0.0.0.0 atau NXDOMAIN
```

### 3. Check Logs

**DNS Server Mandiri:**
```bash
# Linux
sudo journalctl -u dns-server -f

# Windows
# Check Windows Event Viewer atau log file
```

**AdGuard Home:**
```bash
# Linux
sudo journalctl -u AdGuardHome -f

# Atau via Web UI: Query Log tab
```

### 4. Monitor Performance

**AdGuard Dashboard:**
- Buka `http://your-server-ip:80`
- Lihat statistics, query log, top domains

**DNS Server Mandiri Dashboard:**
- Buka `http://your-server-ip:9153`
- Lihat cache stats, QPS, latency histogram

---

## Optimasi untuk 300+ Users

### DNS Server Mandiri (`config-upstream.yaml`)

```yaml
server:
  workers: 8  # Sesuaikan dengan CPU cores

cache:
  max_size: 1000000  # 1 juta entries untuk load tinggi
  prefetch_ratio: 0.15

rate:
  requests_per_sec: 500  # Tingkatkan jika perlu
  burst_size: 1000
```

### AdGuard Home

**Settings → DNS Settings:**
- Cache size: 100000
- Enable "Optimistic cache"
- Enable "Parallel requests" jika ada multiple upstream (tapi tidak perlu untuk setup ini)

**Settings → General Settings:**
- Query log: Limit ke 1000-5000 entries (jangan terlalu besar)
- Statistics interval: 24h atau 7d

---

## Troubleshooting

### AdGuard tidak bisa resolve

**Cek DNS Server Mandiri running:**
```bash
sudo systemctl status dns-server
sudo netstat -tulpn | grep 5353
```

**Test langsung:**
```bash
dig @127.0.0.1 -p 5353 google.com
```

### Latency tinggi

**Cek cache hit rate:**
- DNS Server Mandiri dashboard: `http://localhost:9153`
- Harusnya cache hit rate > 80%

**Tuning:**
```yaml
# config-upstream.yaml
cache:
  prefetch_ratio: 0.2  # Prefetch lebih agresif
  min_ttl: 60s         # TTL minimum lebih tinggi
```

### Memory usage tinggi

**DNS Server Mandiri:**
```yaml
cache:
  max_size: 300000  # Kurangi cache size
```

**AdGuard:**
- Settings → Query log → Reduce retention
- Settings → Statistics → Reduce interval

---

## Monitoring & Maintenance

### Daily Checks

1. **AdGuard Dashboard** - Query count, blocked percentage
2. **DNS Server Mandiri** - Cache hit rate, QPS
3. **System resources** - CPU, memory, network

### Weekly Tasks

1. Update AdGuard filters (otomatis setiap 24h)
2. Check logs untuk errors
3. Review top blocked domains

### Monthly Tasks

1. Review statistics trends
2. Optimize cache size jika perlu
3. Update software (AdGuard + DNS Server Mandiri)

---

## Backup & Recovery

### Backup AdGuard Config

```bash
# Linux
sudo cp /opt/AdGuardHome/AdGuardHome.yaml /backup/
sudo cp -r /opt/AdGuardHome/data /backup/

# Windows
copy C:\AdGuardHome\AdGuardHome.yaml D:\backup\
xcopy C:\AdGuardHome\data D:\backup\data /E /I
```

### Backup DNS Server Mandiri

```bash
# Config
sudo cp /etc/dns-server/config.yaml /backup/

# Cache (opsional, akan rebuild otomatis)
sudo cp /var/lib/dns-server/cache.gob /backup/
```

### Restore

```bash
# Stop services
sudo systemctl stop AdGuardHome dns-server

# Restore files
sudo cp /backup/AdGuardHome.yaml /opt/AdGuardHome/
sudo cp /backup/config.yaml /etc/dns-server/

# Start services
sudo systemctl start dns-server
sudo systemctl start AdGuardHome
```

---

## Migrasi dari Setup Lama

### Dari Pi-hole

1. Export Pi-hole custom DNS records → Import ke AdGuard DNS rewrites
2. Export Pi-hole whitelist/blacklist → Import ke AdGuard
3. Ganti Pi-hole dengan AdGuard + DNS Server Mandiri

### Dari Unbound + Pi-hole

1. Ganti Unbound dengan DNS Server Mandiri
2. Ganti Pi-hole dengan AdGuard Home
3. Konfigurasi sama seperti dokumentasi ini

---

## FAQ

**Q: Apakah bisa pakai dashboard DNS Server Mandiri saja tanpa AdGuard?**  
A: Bisa, tapi dashboard DNS Server Mandiri lebih basic. AdGuard punya UI yang jauh lebih lengkap untuk filtering dan statistics.

**Q: Apakah perlu 2 server terpisah?**  
A: Tidak, bisa di 1 server yang sama. DNS Server Mandiri di port 5353, AdGuard di port 53.

**Q: Bagaimana jika DNS Server Mandiri down?**  
A: AdGuard akan timeout. Bisa tambahkan fallback upstream di AdGuard (8.8.8.8) sebagai backup.

**Q: Apakah bisa pakai DNSSEC?**  
A: Ya, enable di `config-upstream.yaml`:
```yaml
resolver:
  enable_dnssec: true
```
Dan enable DNSSEC di AdGuard settings.

**Q: Berapa resource yang dibutuhkan?**  
A: Untuk 300 users:
- CPU: 2-4 cores
- RAM: 2-4 GB (1GB untuk DNS Server, 1GB untuk AdGuard, sisanya OS)
- Disk: 10GB (untuk logs dan cache)

---

## Support

- **DNS Server Mandiri**: Check dashboard di `http://localhost:9153`
- **AdGuard Home**: https://github.com/AdguardTeam/AdGuardHome/wiki
- **Issues**: Report di repository project ini
