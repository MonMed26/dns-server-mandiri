# DNS Server Mandiri

**High-performance recursive DNS resolver** yang dioptimalkan untuk 300+ pengguna hotspot di belakang Mikrotik. Ditulis dalam Go dengan fitur lengkap seperti Pi-hole.

## 🚀 Fitur Utama

- ✅ **Full Recursive Resolver** - Resolve langsung dari root DNS servers
- ✅ **Cache Massive** - 500K entries dengan prefetch otomatis
- ✅ **DNS Filtering** - Ad/malware blocking seperti Pi-hole
- ✅ **Web Dashboard** - Real-time monitoring & statistics
- ✅ **Failover System** - Fallback ke upstream DNS
- ✅ **Cache Persistence** - Survive server restart
- ✅ **Local DNS Records** - Custom A/AAAA records
- ✅ **Client Statistics** - Per-client tracking
- ✅ **Rate Limiting** - Protection dari DNS flood
- ✅ **EDNS Client Subnet** - Optimal CDN routing

## 📦 Mode Deployment

### Mode 1: Standalone (Recommended untuk Performa)

DNS Server Mandiri berjalan sendiri dengan semua fitur built-in.

```
Clients → DNS Server Mandiri (port 53) → Internet
          [Filtering + Cache + Resolver]
```

**Keuntungan:**
- ⚡ Performa maksimal (no overhead)
- 🎯 Simple setup
- 📊 Dashboard built-in

**Install:**
```bash
make build
sudo make install
sudo systemctl start dns-server
```

### Mode 2: Upstream untuk AdGuard Home

DNS Server Mandiri sebagai recursive resolver backend, AdGuard Home sebagai frontend UI (seperti Unbound + AdGuard).

```
Clients → AdGuard Home (port 53) → DNS Server Mandiri (port 5353) → Internet
          [UI + Filtering]           [Recursive Resolver + Cache]
```

**Keuntungan:**
- 🎨 UI AdGuard yang lebih mature
- 🛡️ Filtering lebih powerful
- 📈 Statistics lebih detail

**Kekurangan:**
- ⚠️ Sedikit overhead latency (~1-2ms)
- 🔧 Maintain 2 services

**Install:**
```bash
sudo bash scripts/install-with-adguard.sh
```

**Dokumentasi:** [ADGUARD-INTEGRATION.md](ADGUARD-INTEGRATION.md)

---

## 🔧 Quick Start (Standalone)

### Linux

```bash
# Clone repository
git clone https://github.com/MonMed26/dns-server-mandiri.git
cd dns-server-mandiri

# Build dan install
make build
sudo make install

# Start service
sudo systemctl start dns-server
sudo systemctl enable dns-server

# Buka dashboard
http://your-server-ip:9153
```

### Windows

```bash
# Build
make build-windows

# Install as Windows Service
.\bin\dns-server-windows.exe install
.\bin\dns-server-windows.exe start

# Buka dashboard
http://localhost:9153
```

### Docker

```bash
docker-compose up -d
```

---

## 📊 Dashboard

Buka `http://your-server-ip:9153` untuk monitoring:

- Real-time QPS & latency
- Cache hit rate & statistics
- Top domains & clients
- Query history
- Filter statistics (blocked queries)

![Dashboard Screenshot](dashboard-screenshot.png)

---

## ⚙️ Konfigurasi

Edit `/etc/dns-server/config.yaml`:

```yaml
server:
  listen_addr: "0.0.0.0"
  udp_port: 53
  tcp_port: 53
  workers: 4

cache:
  max_size: 500000
  default_ttl: 5m

filter:
  enabled: true
  blocklist_dir: "/var/lib/dns-server/blocklists"
  block_response: "zero"  # 0.0.0.0

failover:
  enabled: true
  upstreams:
    - "8.8.8.8"
    - "1.1.1.1"
```

Restart setelah edit:
```bash
sudo systemctl restart dns-server
```

---

## 🔄 Switch Mode

### Dari Standalone ke AdGuard Mode

```bash
sudo bash scripts/install-with-adguard.sh
```

### Dari AdGuard ke Standalone

```bash
sudo bash scripts/uninstall-adguard.sh
```

---

## 🧪 Testing

```bash
# Test DNS resolution
dig @localhost google.com

# Test dari client lain
dig @your-server-ip google.com

# Test filtering (blocked domain)
dig @localhost doubleclick.net

# Benchmark
make bench
```

---

## 📈 Performa

Tested dengan 300+ concurrent users:

- **QPS**: 5000+ queries/second
- **Latency**: <5ms (cache hit), <50ms (cache miss)
- **Cache Hit Rate**: 85-95%
- **Memory**: ~500MB (dengan 500K cache entries)
- **CPU**: <10% (4 workers pada 4-core CPU)

---

## 🛠️ Management

### Logs

```bash
# View logs
sudo journalctl -u dns-server -f

# Last 100 lines
sudo journalctl -u dns-server -n 100
```

### Status

```bash
# Check status
sudo systemctl status dns-server

# Restart
sudo systemctl restart dns-server

# Stop
sudo systemctl stop dns-server
```

### Update

```bash
cd dns-server-mandiri
git pull
make build
sudo systemctl stop dns-server
sudo cp bin/dns-server /usr/local/bin/
sudo systemctl start dns-server
```

---

## 📚 Dokumentasi

- **[ADGUARD-INTEGRATION.md](ADGUARD-INTEGRATION.md)** - Setup dengan AdGuard Home
- **[QUICKSTART-ADGUARD.md](QUICKSTART-ADGUARD.md)** - Quick reference AdGuard
- **[config.yaml](config.yaml)** - Config standalone mode
- **[config-upstream.yaml](config-upstream.yaml)** - Config upstream mode

---

## 🗑️ Uninstall

### Standalone Mode

```bash
sudo make uninstall
```

### Dengan AdGuard

```bash
# Remove AdGuard, keep DNS Server Mandiri
sudo bash scripts/uninstall-adguard.sh

# Remove semua
sudo make uninstall
sudo systemctl stop AdGuardHome
sudo rm -rf /opt/AdGuardHome
```

---

## 🐛 Troubleshooting

### Port 53 sudah dipakai

```bash
# Cek service yang pakai port 53
sudo netstat -tulpn | grep :53

# Stop systemd-resolved
sudo systemctl stop systemd-resolved
sudo systemctl disable systemd-resolved
```

### DNS tidak resolve

```bash
# Check service
sudo systemctl status dns-server

# Check logs
sudo journalctl -u dns-server -n 50

# Test langsung
dig @127.0.0.1 google.com
```

### Memory tinggi

```yaml
# Edit /etc/dns-server/config.yaml
cache:
  max_size: 300000  # Kurangi dari 500000
```

---

## 🤝 Contributing

Pull requests welcome! Untuk perubahan besar, buka issue dulu untuk diskusi.

---

## 📝 License

MIT License - lihat [LICENSE](LICENSE) file

---

## 🙏 Credits

- [miekg/dns](https://github.com/miekg/dns) - DNS library untuk Go
- [AdGuard Home](https://github.com/AdguardTeam/AdGuardHome) - Inspirasi untuk filtering
- [Pi-hole](https://pi-hole.net/) - Inspirasi untuk dashboard

---

## 📞 Support

- **Issues**: [GitHub Issues](https://github.com/MonMed26/dns-server-mandiri/issues)
- **Dashboard**: http://localhost:9153
- **Logs**: `sudo journalctl -u dns-server -f`

---

**Made with ❤️ for Indonesian ISP & Hotspot operators**
