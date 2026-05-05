package resolver

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"dns-server-mandiri/internal/cache"
	"dns-server-mandiri/internal/config"

	"github.com/miekg/dns"
)

// Resolver performs iterative DNS resolution starting from root servers
type Resolver struct {
	cache   *cache.Cache
	cfg     config.ResolverConfig
	client  *dns.Client
	clientTCP *dns.Client
	logger  *slog.Logger

	// Inflight deduplication - prevent multiple identical queries
	inflight   map[string]*inflightEntry
	inflightMu sync.Mutex
}

type inflightEntry struct {
	done chan struct{}
	msg  *dns.Msg
	err  error
}

// New creates a new recursive resolver
func New(c *cache.Cache, cfg config.ResolverConfig, logger *slog.Logger) *Resolver {
	return &Resolver{
		cache: c,
		cfg:   cfg,
		client: &dns.Client{
			Net:          "udp",
			Timeout:      cfg.Timeout,
			ReadTimeout:  cfg.Timeout,
			WriteTimeout: cfg.Timeout,
			UDPSize:      4096,
		},
		clientTCP: &dns.Client{
			Net:          "tcp",
			Timeout:      cfg.Timeout * 2,
			ReadTimeout:  cfg.Timeout * 2,
			WriteTimeout: cfg.Timeout * 2,
		},
		logger:   logger,
		inflight: make(map[string]*inflightEntry),
	}
}

// Resolve performs full recursive resolution for a DNS query
func (r *Resolver) Resolve(ctx context.Context, name string, qtype uint16, qclass uint16) (*dns.Msg, error) {
	name = dns.Fqdn(name)

	// Check cache first
	if msg, found, shouldPrefetch := r.cache.Get(name, qtype, qclass); found {
		if shouldPrefetch {
			// Trigger async prefetch
			go r.prefetch(name, qtype, qclass)
		}
		return msg, nil
	}

	// Deduplicate inflight requests
	key := fmt.Sprintf("%s/%d/%d", name, qtype, qclass)
	r.inflightMu.Lock()
	if entry, exists := r.inflight[key]; exists {
		r.inflightMu.Unlock()
		// Wait for the existing request to complete
		select {
		case <-entry.done:
			if entry.msg != nil {
				return entry.msg.Copy(), entry.err
			}
			return nil, entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	entry := &inflightEntry{done: make(chan struct{})}
	r.inflight[key] = entry
	r.inflightMu.Unlock()

	// Perform resolution
	msg, err := r.resolveIterative(ctx, name, qtype, qclass, 0)

	// Store result and notify waiters
	entry.msg = msg
	entry.err = err
	close(entry.done)

	// Remove from inflight
	r.inflightMu.Lock()
	delete(r.inflight, key)
	r.inflightMu.Unlock()

	// Cache the result
	if msg != nil && err == nil {
		r.cache.Set(name, qtype, qclass, msg)
	}

	return msg, err
}

// resolveIterative performs iterative resolution starting from root servers
func (r *Resolver) resolveIterative(ctx context.Context, name string, qtype uint16, qclass uint16, depth int) (*dns.Msg, error) {
	if depth > r.cfg.MaxDepth {
		return nil, fmt.Errorf("max resolution depth exceeded for %s", name)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Start with root servers
	nameservers := r.getRootServers()

	// Iterative resolution loop
	for i := 0; i < r.cfg.MaxDepth; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Query the current set of nameservers
		resp, err := r.queryNameservers(ctx, name, qtype, nameservers)
		if err != nil {
			r.logger.Debug("query nameservers failed", "name", name, "error", err)
			return nil, err
		}

		if resp == nil {
			return nil, fmt.Errorf("no response from nameservers for %s", name)
		}

		// Case 1: Got an authoritative answer or NXDOMAIN
		if resp.Authoritative || resp.Rcode == dns.RcodeNameError {
			return r.buildResponse(name, qtype, qclass, resp), nil
		}

		// Case 2: Got an answer (non-authoritative but has answers)
		if len(resp.Answer) > 0 {
			// Check for CNAME and follow it
			if qtype != dns.TypeCNAME {
				if cname := r.extractCNAME(resp, name); cname != "" {
					return r.followCNAME(ctx, name, cname, qtype, qclass, resp, depth)
				}
			}
			return r.buildResponse(name, qtype, qclass, resp), nil
		}

		// Case 3: Got a referral (NS records in authority section)
		if len(resp.Ns) > 0 {
			newNS := r.extractReferral(ctx, resp, depth)
			if len(newNS) > 0 {
				nameservers = newNS
				continue
			}
		}

		// No useful response
		return r.buildResponse(name, qtype, qclass, resp), nil
	}

	return nil, fmt.Errorf("resolution loop exhausted for %s", name)
}

// queryNameservers queries a list of nameservers and returns the first successful response
func (r *Resolver) queryNameservers(ctx context.Context, name string, qtype uint16, nameservers []string) (*dns.Msg, error) {
	// Shuffle nameservers for load distribution
	shuffled := make([]string, len(nameservers))
	copy(shuffled, nameservers)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	msg := NewRootQuery(name, qtype)

	var lastErr error
	for _, ns := range shuffled {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		addr := net.JoinHostPort(ns, "53")

		// Try UDP first
		for retry := 0; retry <= r.cfg.Retries; retry++ {
			resp, _, err := r.client.ExchangeContext(ctx, msg, addr)
			if err != nil {
				lastErr = err
				continue
			}

			// If truncated, retry with TCP
			if resp.Truncated {
				resp, _, err = r.clientTCP.ExchangeContext(ctx, msg, addr)
				if err != nil {
					lastErr = err
					continue
				}
			}

			// Valid response
			if resp.Rcode == dns.RcodeSuccess || resp.Rcode == dns.RcodeNameError {
				return resp, nil
			}

			// SERVFAIL - try next server
			if resp.Rcode == dns.RcodeServerFailure {
				lastErr = fmt.Errorf("SERVFAIL from %s", ns)
				break // try next nameserver
			}

			return resp, nil
		}
	}

	return nil, fmt.Errorf("all nameservers failed for %s: %v", name, lastErr)
}

// extractCNAME extracts a CNAME target from the response
func (r *Resolver) extractCNAME(resp *dns.Msg, name string) string {
	for _, rr := range resp.Answer {
		if cname, ok := rr.(*dns.CNAME); ok {
			if dns.CanonicalName(cname.Hdr.Name) == dns.CanonicalName(name) {
				return cname.Target
			}
		}
	}
	return ""
}

// followCNAME resolves a CNAME chain
func (r *Resolver) followCNAME(ctx context.Context, originalName, cname string, qtype uint16, qclass uint16, resp *dns.Msg, depth int) (*dns.Msg, error) {
	if depth > r.cfg.MaxCNAMEChain {
		return nil, fmt.Errorf("CNAME chain too long for %s", originalName)
	}

	// Resolve the CNAME target
	cnameResp, err := r.resolveIterative(ctx, cname, qtype, qclass, depth+1)
	if err != nil {
		return nil, err
	}

	// Build combined response with CNAME chain
	result := new(dns.Msg)
	result.SetReply(NewRootQuery(originalName, qtype))
	result.Authoritative = false
	result.RecursionAvailable = true

	// Add CNAME records from original response
	for _, rr := range resp.Answer {
		if _, ok := rr.(*dns.CNAME); ok {
			result.Answer = append(result.Answer, rr)
		}
	}

	// Add answers from CNAME resolution
	if cnameResp != nil {
		result.Answer = append(result.Answer, cnameResp.Answer...)
		result.Ns = cnameResp.Ns
		result.Extra = cnameResp.Extra
		result.Rcode = cnameResp.Rcode
	}

	return result, nil
}

// extractReferral extracts nameserver IPs from a referral response
func (r *Resolver) extractReferral(ctx context.Context, resp *dns.Msg, depth int) []string {
	var nsNames []string
	var nsIPs []string

	// Extract NS names from authority section
	for _, rr := range resp.Ns {
		if ns, ok := rr.(*dns.NS); ok {
			nsNames = append(nsNames, ns.Ns)
		}
	}

	if len(nsNames) == 0 {
		return nil
	}

	// Look for glue records (A/AAAA in additional section)
	glueMap := make(map[string][]string)
	for _, rr := range resp.Extra {
		switch v := rr.(type) {
		case *dns.A:
			glueMap[dns.CanonicalName(v.Hdr.Name)] = append(glueMap[dns.CanonicalName(v.Hdr.Name)], v.A.String())
		case *dns.AAAA:
			glueMap[dns.CanonicalName(v.Hdr.Name)] = append(glueMap[dns.CanonicalName(v.Hdr.Name)], v.AAAA.String())
		}
	}

	// Use glue records if available
	for _, nsName := range nsNames {
		canonical := dns.CanonicalName(nsName)
		if ips, ok := glueMap[canonical]; ok {
			nsIPs = append(nsIPs, ips...)
		}
	}

	// If no glue records, resolve NS names
	if len(nsIPs) == 0 {
		for _, nsName := range nsNames {
			// Check cache first
			if msg, found, _ := r.cache.Get(nsName, dns.TypeA, dns.ClassINET); found {
				for _, rr := range msg.Answer {
					if a, ok := rr.(*dns.A); ok {
						nsIPs = append(nsIPs, a.A.String())
					}
				}
			} else if depth < r.cfg.MaxDepth-5 {
				// Resolve the NS name (be careful of infinite loops)
				nsResp, err := r.resolveIterative(ctx, nsName, dns.TypeA, dns.ClassINET, depth+1)
				if err == nil && nsResp != nil {
					for _, rr := range nsResp.Answer {
						if a, ok := rr.(*dns.A); ok {
							nsIPs = append(nsIPs, a.A.String())
						}
					}
					// Cache the NS resolution
					r.cache.Set(nsName, dns.TypeA, dns.ClassINET, nsResp)
				}
			}
		}
	}

	return nsIPs
}

// buildResponse constructs a proper DNS response message
func (r *Resolver) buildResponse(name string, qtype uint16, qclass uint16, resp *dns.Msg) *dns.Msg {
	result := new(dns.Msg)
	result.SetReply(NewRootQuery(name, qtype))
	result.RecursionAvailable = true
	result.Authoritative = false
	result.Rcode = resp.Rcode
	result.Answer = resp.Answer
	result.Ns = resp.Ns
	result.Extra = resp.Extra

	// Remove OPT records from extra (we'll add our own if needed)
	var cleanExtra []dns.RR
	for _, rr := range result.Extra {
		if rr.Header().Rrtype != dns.TypeOPT {
			cleanExtra = append(cleanExtra, rr)
		}
	}
	result.Extra = cleanExtra

	return result
}

// getRootServers returns the list of root server IPs
func (r *Resolver) getRootServers() []string {
	return RootServers
}

// prefetch resolves a query in the background to refresh the cache
func (r *Resolver) prefetch(name string, qtype uint16, qclass uint16) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msg, err := r.resolveIterative(ctx, name, qtype, qclass, 0)
	if err == nil && msg != nil {
		r.cache.Set(name, qtype, qclass, msg)
		r.logger.Debug("prefetched", "name", name, "type", dns.TypeToString[qtype])
	}
}

// PrefetchPopular prefetches popular cached entries before they expire
func (r *Resolver) PrefetchPopular() {
	candidates := r.cache.PrefetchCandidates()
	for _, c := range candidates {
		go r.prefetch(c.Name, c.Qtype, c.Qclass)
	}
}

// ResolveWithType is a helper that resolves and returns specific record types
func (r *Resolver) ResolveWithType(ctx context.Context, name string, qtype uint16) ([]string, error) {
	msg, err := r.Resolve(ctx, name, qtype, dns.ClassINET)
	if err != nil {
		return nil, err
	}

	var results []string
	for _, rr := range msg.Answer {
		results = append(results, strings.TrimPrefix(rr.String(), rr.Header().String()))
	}
	return results, nil
}
