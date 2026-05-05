package resolver

import "github.com/miekg/dns"

// RootServers contains the IP addresses of the 13 root DNS servers
// These are the starting point for recursive resolution
// Updated as of 2024 - these rarely change
var RootServers = []string{
	"198.41.0.4",     // a.root-servers.net (Verisign)
	"170.247.170.2",  // b.root-servers.net (USC-ISI)
	"192.33.4.12",    // c.root-servers.net (Cogent)
	"199.7.91.13",    // d.root-servers.net (UMD)
	"192.203.230.10", // e.root-servers.net (NASA)
	"192.5.5.241",    // f.root-servers.net (ISC)
	"192.112.36.4",   // g.root-servers.net (DISA)
	"198.97.190.53",  // h.root-servers.net (US Army)
	"192.36.148.17",  // i.root-servers.net (Netnod)
	"192.58.128.30",  // j.root-servers.net (Verisign)
	"193.0.14.129",   // k.root-servers.net (RIPE NCC)
	"199.7.83.42",    // l.root-servers.net (ICANN)
	"202.12.27.33",   // m.root-servers.net (WIDE)
}

// RootServersV6 contains IPv6 addresses of root servers
var RootServersV6 = []string{
	"2001:503:ba3e::2:30", // a.root-servers.net
	"2001:500:200::b",     // b.root-servers.net
	"2001:500:2::c",       // c.root-servers.net
	"2001:500:2d::d",      // d.root-servers.net
	"2001:500:a8::e",      // e.root-servers.net
	"2001:500:2f::f",      // f.root-servers.net
	"2001:500:12::d0d",    // g.root-servers.net
	"2001:500:1::53",      // h.root-servers.net
	"2001:7fe::53",        // i.root-servers.net
	"2001:503:c27::2:30",  // j.root-servers.net
	"2001:7fd::1",         // k.root-servers.net
	"2001:500:9f::42",     // l.root-servers.net
	"2001:dc3::35",        // m.root-servers.net
}

// NewRootQuery creates a DNS query message
func NewRootQuery(name string, qtype uint16) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), qtype)
	msg.RecursionDesired = false // We do iterative resolution
	return msg
}
