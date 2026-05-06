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

## 🏗️ Arsitektur

```
Clients → DNS Server Mandiri (port 53) → Internet
          [Filtering + Cache + Resolver]
```

**Keuntungan:**
- ⚡ Performa maksimal (no overhead)
- 🎯 Simple setup & maintenance
- 📊 Dashboard built-in
- 🛡️ Privacy-focused (no third-party DNS)

---

## 🔧 Quick Start

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

## 🧪 Testing & Monitoring

### Test DNS Resolution

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

### Monitoring

**Dashboard**: http://your-server-ip:9153

**Logs:**
```bash
# View logs
sudo journalctl -u dns-server -f

# Last 100 lines
sudo journalctl -u dns-server -n 100
```

**Status:**
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

## 📈 Performa

Tested dengan 300+ concurrent users:

- **QPS**: 5000+ queries/second
- **Latency**: <5ms (cache hit), <50ms (cache miss)
- **Cache Hit Rate**: 85-95%
- **Memory**: ~500MB (dengan 500K cache entries)
- **CPU**: <10% (4 workers pada 4-core CPU)

---

## 🗑️ Uninstall

```bash
sudo make uninstall
```

Atau manual:
```bash
sudo systemctl stop dns-server
sudo systemctl disable dns-server
sudo rm -f /usr/local/bin/dns-server
sudo rm -rf /etc/dns-server /var/lib/dns-server
sudo rm -f /etc/systemd/system/dns-server.service
sudo userdel dns-server
sudo systemctl daemon-reload
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

MIT License

---

## 🙏 Credits

- [miekg/dns](https://github.com/miekg/dns) - DNS library untuk Go
- [Pi-hole](https://pi-hole.net/) - Inspirasi untuk filtering & dashboard

---

## 📞 Support

- **Issues**: [GitHub Issues](https://github.com/MonMed26/dns-server-mandiri/issues)
- **Dashboard**: http://localhost:9153
- **Logs**: `sudo journalctl -u dns-server -f`

---

**Made with ❤️ for Indonesian ISP & Hotspot operators**
