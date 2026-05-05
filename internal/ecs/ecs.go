package ecs

import (
	"net"

	"github.com/miekg/dns"
)

// Config for EDNS Client Subnet
type Config struct {
	Enabled    bool   `yaml:"enabled"`
	SubnetIPv4 int    `yaml:"subnet_ipv4"` // prefix length for IPv4 (default: 24)
	SubnetIPv6 int    `yaml:"subnet_ipv6"` // prefix length for IPv6 (default: 56)
}

// DefaultECSConfig returns default ECS configuration
func DefaultECSConfig() Config {
	return Config{
		Enabled:    true,
		SubnetIPv4: 24,
		SubnetIPv6: 56,
	}
}

// Handler manages EDNS Client Subnet
type Handler struct {
	cfg Config
}

// New creates a new ECS handler
func New(cfg Config) *Handler {
	return &Handler{cfg: cfg}
}

// AddClientSubnet adds EDNS Client Subnet option to a DNS query
// This tells authoritative servers the approximate location of the client
// so CDNs can return geographically closer servers
func (h *Handler) AddClientSubnet(msg *dns.Msg, clientIP string) {
	if !h.cfg.Enabled || clientIP == "" {
		return
	}

	ip := net.ParseIP(clientIP)
	if ip == nil {
		return
	}

	// Skip private/local IPs
	if isPrivateIP(ip) {
		return
	}

	var family uint16
	var sourceNetmask uint8
	var address net.IP

	if ip4 := ip.To4(); ip4 != nil {
		family = 1 // IPv4
		sourceNetmask = uint8(h.cfg.SubnetIPv4)
		// Mask the IP to the specified prefix length
		mask := net.CIDRMask(h.cfg.SubnetIPv4, 32)
		address = ip4.Mask(mask)
	} else {
		family = 2 // IPv6
		sourceNetmask = uint8(h.cfg.SubnetIPv6)
		mask := net.CIDRMask(h.cfg.SubnetIPv6, 128)
		address = ip.Mask(mask)
	}

	// Create EDNS0 subnet option
	ecs := &dns.EDNS0_SUBNET{
		Code:          dns.EDNS0SUBNET,
		Family:        family,
		SourceNetmask: sourceNetmask,
		SourceScope:   0,
		Address:       address,
	}

	// Find or create OPT record
	opt := msg.IsEdns0()
	if opt == nil {
		opt = &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
		}
		opt.SetUDPSize(4096)
		msg.Extra = append(msg.Extra, opt)
	}

	// Add ECS option
	opt.Option = append(opt.Option, ecs)
}

// ExtractClientSubnet extracts ECS info from a response (for caching purposes)
func (h *Handler) ExtractClientSubnet(msg *dns.Msg) (net.IP, uint8, bool) {
	if !h.cfg.Enabled {
		return nil, 0, false
	}

	opt := msg.IsEdns0()
	if opt == nil {
		return nil, 0, false
	}

	for _, option := range opt.Option {
		if ecs, ok := option.(*dns.EDNS0_SUBNET); ok {
			return ecs.Address, ecs.SourceScope, true
		}
	}

	return nil, 0, false
}

// IsEnabled returns whether ECS is enabled
func (h *Handler) IsEnabled() bool {
	return h.cfg.Enabled
}

// isPrivateIP checks if an IP is private/local
func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{parseCIDR("10.0.0.0/8")},
		{parseCIDR("172.16.0.0/12")},
		{parseCIDR("192.168.0.0/16")},
		{parseCIDR("127.0.0.0/8")},
		{parseCIDR("169.254.0.0/16")},
		{parseCIDR("fc00::/7")},
		{parseCIDR("fe80::/10")},
		{parseCIDR("::1/128")},
	}

	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDR(s string) *net.IPNet {
	_, network, _ := net.ParseCIDR(s)
	return network
}
