package localrecords

import (
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

// Record represents a local DNS record
type Record struct {
	Name   string `json:"name" yaml:"name"`
	Type   string `json:"type" yaml:"type"`     // A, AAAA, CNAME, TXT, MX
	Value  string `json:"value" yaml:"value"`
	TTL    uint32 `json:"ttl" yaml:"ttl"`
}

// Config for local records
type Config struct {
	Enabled bool     `yaml:"enabled"`
	Records []Record `yaml:"records"`
}

// DefaultLocalRecordsConfig returns default config
func DefaultLocalRecordsConfig() Config {
	return Config{
		Enabled: true,
		Records: []Record{},
	}
}

// LocalRecords manages custom local DNS records
type LocalRecords struct {
	mu      sync.RWMutex
	records map[string][]dns.RR // key: "name/type"
	logger  *slog.Logger
	enabled bool
}

// New creates a new local records handler
func New(cfg Config, logger *slog.Logger) *LocalRecords {
	lr := &LocalRecords{
		records: make(map[string][]dns.RR),
		logger:  logger,
		enabled: cfg.Enabled,
	}

	// Load initial records from config
	for _, r := range cfg.Records {
		lr.AddRecord(r)
	}

	return lr
}

// Lookup checks if there's a local record for the query
func (lr *LocalRecords) Lookup(name string, qtype uint16) (*dns.Msg, bool) {
	if !lr.enabled {
		return nil, false
	}

	name = dns.CanonicalName(name)
	key := name + "/" + dns.TypeToString[qtype]

	lr.mu.RLock()
	rrs, exists := lr.records[key]
	lr.mu.RUnlock()

	if !exists || len(rrs) == 0 {
		// Check for CNAME if A/AAAA not found
		if qtype == dns.TypeA || qtype == dns.TypeAAAA {
			cnameKey := name + "/CNAME"
			lr.mu.RLock()
			cnameRRs, cnameExists := lr.records[cnameKey]
			lr.mu.RUnlock()
			if cnameExists {
				rrs = cnameRRs
				exists = true
			}
		}
		if !exists {
			return nil, false
		}
	}

	// Build response
	msg := new(dns.Msg)
	msg.Authoritative = true
	msg.RecursionAvailable = true
	msg.Answer = make([]dns.RR, len(rrs))
	copy(msg.Answer, rrs)

	return msg, true
}

// AddRecord adds a local DNS record
func (lr *LocalRecords) AddRecord(r Record) error {
	name := dns.CanonicalName(strings.ToLower(r.Name))
	if !strings.HasSuffix(name, ".") {
		name += "."
	}

	ttl := r.TTL
	if ttl == 0 {
		ttl = 300 // default 5 minutes
	}

	var rr dns.RR
	switch strings.ToUpper(r.Type) {
	case "A":
		ip := net.ParseIP(r.Value)
		if ip == nil || ip.To4() == nil {
			return &InvalidRecordError{Msg: "invalid IPv4 address: " + r.Value}
		}
		rr = &dns.A{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
			A:   ip.To4(),
		}
	case "AAAA":
		ip := net.ParseIP(r.Value)
		if ip == nil || ip.To16() == nil {
			return &InvalidRecordError{Msg: "invalid IPv6 address: " + r.Value}
		}
		rr = &dns.AAAA{
			Hdr:  dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: ttl},
			AAAA: ip.To16(),
		}
	case "CNAME":
		target := dns.CanonicalName(r.Value)
		if !strings.HasSuffix(target, ".") {
			target += "."
		}
		rr = &dns.CNAME{
			Hdr:    dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: ttl},
			Target: target,
		}
	case "TXT":
		rr = &dns.TXT{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: ttl},
			Txt: []string{r.Value},
		}
	case "MX":
		rr = &dns.MX{
			Hdr:        dns.RR_Header{Name: name, Rrtype: dns.TypeMX, Class: dns.ClassINET, Ttl: ttl},
			Preference: 10,
			Mx:         dns.CanonicalName(r.Value),
		}
	default:
		return &InvalidRecordError{Msg: "unsupported record type: " + r.Type}
	}

	key := name + "/" + strings.ToUpper(r.Type)

	lr.mu.Lock()
	lr.records[key] = append(lr.records[key], rr)
	lr.mu.Unlock()

	lr.logger.Info("local record added", "name", name, "type", r.Type, "value", r.Value)
	return nil
}

// RemoveRecord removes a local DNS record
func (lr *LocalRecords) RemoveRecord(name string, recordType string) {
	name = dns.CanonicalName(strings.ToLower(name))
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	key := name + "/" + strings.ToUpper(recordType)

	lr.mu.Lock()
	delete(lr.records, key)
	lr.mu.Unlock()
}

// GetAllRecords returns all configured local records
func (lr *LocalRecords) GetAllRecords() []Record {
	lr.mu.RLock()
	defer lr.mu.RUnlock()

	var records []Record
	for _, rrs := range lr.records {
		for _, rr := range rrs {
			hdr := rr.Header()
			var value string
			switch v := rr.(type) {
			case *dns.A:
				value = v.A.String()
			case *dns.AAAA:
				value = v.AAAA.String()
			case *dns.CNAME:
				value = v.Target
			case *dns.TXT:
				value = strings.Join(v.Txt, " ")
			case *dns.MX:
				value = v.Mx
			}
			records = append(records, Record{
				Name:  hdr.Name,
				Type:  dns.TypeToString[hdr.Rrtype],
				Value: value,
				TTL:   hdr.Ttl,
			})
		}
	}
	return records
}

// IsEnabled returns whether local records are enabled
func (lr *LocalRecords) IsEnabled() bool {
	return lr.enabled
}

// InvalidRecordError represents an invalid record error
type InvalidRecordError struct {
	Msg string
}

func (e *InvalidRecordError) Error() string {
	return e.Msg
}
