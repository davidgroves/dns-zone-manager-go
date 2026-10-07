package dnsx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

// DNSClientError is the base error for DNS client failures.
type DNSClientError struct {
	Message string
	Cause   error
}

func (e *DNSClientError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *DNSClientError) Unwrap() error { return e.Cause }

// ZoneTransferError indicates AXFR/IXFR failure.
type ZoneTransferError struct {
	Message string
	Cause   error
}

func (e *ZoneTransferError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *ZoneTransferError) Unwrap() error { return e.Cause }

// PrerequisiteError indicates a DDNS prerequisite failure (NXRRSET, YXRRSET, etc.).
type PrerequisiteError struct {
	Message   string
	Rcode     int
	RcodeText string
}

func (e *PrerequisiteError) Error() string { return e.Message }

// UpdateError indicates a non-prerequisite DDNS failure.
type UpdateError struct {
	Message string
	Cause   error
	Rcode   int
}

func (e *UpdateError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *UpdateError) Unwrap() error { return e.Cause }

// ChangeHooks receives notifications after each DDNS exchange.
type ChangeHooks interface {
	OnUpdateResult(ctx context.Context, zone string, msg *dns.Msg, resp *dns.Msg, err error)
}

// TransferBackend is the subset of Client used by ZoneCache (for test fakes).
type TransferBackend interface {
	PerformAXFR(ctx context.Context, zone string) (*Zone, error)
	PerformIXFR(ctx context.Context, zone string, fromSerial uint32) (IXFRResult, error)
	QuerySOA(ctx context.Context, zone string) (serial uint32, err error)
}

// Client performs DNS queries, zone transfers, and DDNS updates.
type Client struct {
	settings *config.Settings
	serverIP string
	udpAddr  string
	tcpAddr  string
	timeout  time.Duration
	axfrTO   time.Duration

	tsigSecret    map[string]string
	updateKeyName string
	updateAlg     string
	axfrKeyName   string
	axfrAlg       string

	pool  *Pool
	Hooks ChangeHooks
	log   *slog.Logger
}

// Ensure Client implements TransferBackend.
var _ TransferBackend = (*Client)(nil)

// NewClient resolves the DNS server, builds a TCP pool, and stores TSIG secrets.
func NewClient(settings *config.Settings) (*Client, error) {
	if settings == nil {
		return nil, fmt.Errorf("settings is nil")
	}
	updateKey := settings.GetUpdateTSIGKey()
	if updateKey == nil {
		return nil, fmt.Errorf("TSIG key %q not found in tsig_keys", settings.DNS.UpdateTSIGKey)
	}

	ip, err := resolveServerAddress(settings.DNS.Server)
	if err != nil {
		return nil, err
	}
	if ip != settings.DNS.Server {
		logging.LogInternalEvent(nil, "dns_server_resolved", slog.LevelInfo,
			slog.String("server_host", settings.DNS.Server),
			slog.String("server_ip", ip),
		)
	}

	tcpPort := settings.DNS.EffectiveTCPPort()
	udpPort := settings.DNS.Port
	dialTO := settings.DNS.Timeout
	if dialTO <= 0 {
		dialTO = 10 * time.Second
	}

	secrets := BuildTSIGMap(settings.TSIGKeys)
	axfrKey := settings.GetAXFRTSIGKey()
	axfrName := dns.Fqdn(updateKey.Name)
	axfrAlg := AlgorithmFromString(updateKey.Algorithm)
	if axfrKey != nil {
		axfrName = dns.Fqdn(axfrKey.Name)
		axfrAlg = AlgorithmFromString(axfrKey.Algorithm)
	}

	c := &Client{
		settings:      settings,
		serverIP:      ip,
		udpAddr:       net.JoinHostPort(ip, fmt.Sprintf("%d", udpPort)),
		tcpAddr:       net.JoinHostPort(ip, fmt.Sprintf("%d", tcpPort)),
		timeout:       settings.DNS.Timeout,
		axfrTO:        settings.DNS.AXFRTimeout,
		tsigSecret:    secrets,
		updateKeyName: dns.Fqdn(updateKey.Name),
		updateAlg:     AlgorithmFromString(updateKey.Algorithm),
		axfrKeyName:   axfrName,
		axfrAlg:       axfrAlg,
		pool: NewPool(
			net.JoinHostPort(ip, fmt.Sprintf("%d", tcpPort)),
			settings.DNS.PoolSize,
			settings.DNS.PoolIdleTimeout,
			dialTO,
		),
		log: logging.Default(),
	}
	return c, nil
}

// ServerIP returns the resolved DNS server address.
func (c *Client) ServerIP() string { return c.serverIP }

// Close shuts down the TCP connection pool.
func (c *Client) Close() {
	if c.pool != nil {
		c.pool.Close()
	}
}

func resolveServerAddress(server string) (string, error) {
	if ip := net.ParseIP(server); ip != nil {
		return server, nil
	}
	addrs, err := net.LookupIP(server)
	if err != nil {
		return "", fmt.Errorf("could not resolve DNS server hostname %q: %w", server, err)
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0].String(), nil
	}
	return "", fmt.Errorf("could not resolve DNS server hostname %q: no addresses returned", server)
}

// CheckServerResponding probes the server with a UDP SOA query for ".".
// Any DNS response (including REFUSED) counts as up.
func (c *Client) CheckServerResponding(ctx context.Context) bool {
	m := new(dns.Msg)
	m.SetQuestion(".", dns.TypeSOA)
	client := &dns.Client{Net: "udp", Timeout: c.timeout}
	if deadline, ok := ctx.Deadline(); ok {
		client.Timeout = time.Until(deadline)
	}
	_, _, err := client.ExchangeContext(ctx, m, c.udpAddr)
	return err == nil
}

// QuerySOA returns the SOA serial for zone via UDP.
func (c *Client) QuerySOA(ctx context.Context, zone string) (uint32, error) {
	soa, err := c.querySOA(ctx, zone)
	if err != nil {
		return 0, err
	}
	return soa.Serial, nil
}

// querySOA fetches the apex SOA. IXFR copies its nameserver and mailbox into
// the authority section; BIND rejects an SOA with those names empty (FORMERR).
func (c *Client) querySOA(ctx context.Context, zone string) (*dns.SOA, error) {
	zone = NormalizeZoneName(zone)
	m := new(dns.Msg)
	m.SetQuestion(zone, dns.TypeSOA)
	client := &dns.Client{Net: "udp", Timeout: c.timeout}
	resp, _, err := client.ExchangeContext(ctx, m, c.udpAddr)
	if err != nil {
		return nil, &DNSClientError{Message: "SOA query failed", Cause: err}
	}
	if resp.Rcode != dns.RcodeSuccess {
		return nil, &DNSClientError{
			Message: fmt.Sprintf("SOA query rcode %s", dns.RcodeToString[resp.Rcode]),
		}
	}
	for _, rr := range resp.Answer {
		if soa, ok := rr.(*dns.SOA); ok {
			return soa, nil
		}
	}
	return nil, &DNSClientError{Message: "SOA query returned no SOA record"}
}

// ixfrMsg builds an IXFR query. The authority SOA keeps the client's serial
// and the zone SOA's primary nameserver and mailbox.
func ixfrMsg(zone string, fromSerial uint32, soa *dns.SOA) *dns.Msg {
	ns, mbox := "", ""
	if soa != nil {
		ns, mbox = soa.Ns, soa.Mbox
	}
	m := new(dns.Msg)
	m.SetIxfr(zone, fromSerial, ns, mbox)
	return m
}

// PerformAXFR transfers the full zone over TCP with TSIG.
func (c *Client) PerformAXFR(ctx context.Context, zone string) (*Zone, error) {
	zone = NormalizeZoneName(zone)
	maxBytes := c.settings.Cache.EffectiveMaxZoneSizeBytes()

	logging.LogInternalEvent(c.log, "axfr_start", slog.LevelInfo,
		slog.String("zone", zone),
		slog.String("server", c.serverIP),
		slog.String("addr", c.tcpAddr),
		slog.Int64("max_bytes", maxBytes),
	)

	m := new(dns.Msg)
	m.SetAxfr(zone)
	c.attachTSIG(m, c.axfrKeyName, c.axfrAlg)

	trs := &dns.Transfer{
		DialTimeout: c.timeout,
		ReadTimeout: c.axfrTO,
		TsigSecret:  c.tsigSecret,
	}
	if deadline, ok := ctx.Deadline(); ok {
		trs.ReadTimeout = time.Until(deadline)
	}

	env, err := trs.In(m, c.tcpAddr)
	if err != nil {
		metrics.IncZoneTransfersFailed("axfr", zone)
		logging.LogInternalEvent(c.log, "axfr_failed", slog.LevelError,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		return nil, &ZoneTransferError{Message: fmt.Sprintf("zone transfer failed for %s", zone), Cause: err}
	}

	var rrs []dns.RR
	var total int64
	for e := range env {
		if e.Error != nil {
			metrics.IncZoneTransfersFailed("axfr", zone)
			return nil, &ZoneTransferError{
				Message: fmt.Sprintf("zone transfer failed for %s", zone),
				Cause:   e.Error,
			}
		}
		for _, rr := range e.RR {
			if rr == nil || rr.Header().Rrtype == dns.TypeTSIG {
				continue
			}
			total += int64(dns.Len(rr))
			if maxBytes > 0 && total > maxBytes {
				metrics.IncZoneTransfersAborted("axfr", "size_limit")
				return nil, &ZoneTransferError{
					Message: fmt.Sprintf(
						"AXFR for %s exceeded max_zone_size_bytes (%d > %d)",
						zone, total, maxBytes,
					),
				}
			}
			rrs = append(rrs, rr)
		}
	}

	z := NewZone(zone)
	if err := z.SetFromAXFR(rrs); err != nil {
		return nil, &ZoneTransferError{Message: fmt.Sprintf("failed to build zone %s", zone), Cause: err}
	}
	metrics.IncZoneTransfers("axfr", zone)
	logging.LogInternalEvent(c.log, "axfr_complete", slog.LevelInfo,
		slog.String("zone", zone),
		slog.Int("rrsets", z.RRsetCount()),
	)
	return z, nil
}

// PerformIXFR requests an incremental transfer from fromSerial.
func (c *Client) PerformIXFR(ctx context.Context, zone string, fromSerial uint32) (IXFRResult, error) {
	zone = NormalizeZoneName(zone)

	logging.LogInternalEvent(c.log, "ixfr_start", slog.LevelInfo,
		slog.String("zone", zone),
		slog.Uint64("from_serial", uint64(fromSerial)),
		slog.String("server", c.serverIP),
	)

	// Serial eligibility against live SOA when possible. The same SOA supplies
	// MNAME and RNAME for the IXFR authority section.
	var soa *dns.SOA
	if looked, err := c.querySOA(ctx, zone); err == nil {
		soa = looked
	}
	if fromSerial != 0 && soa != nil {
		if fromSerial == soa.Serial {
			return IXFRResult{NewSerial: soa.Serial}, nil
		}
		if !Less(fromSerial, soa.Serial) {
			return IXFRResult{}, &ZoneTransferError{
				Message: fmt.Sprintf(
					"IXFR not eligible: from_serial=%d not less than current=%d",
					fromSerial, soa.Serial,
				),
			}
		}
	}

	m := ixfrMsg(zone, fromSerial, soa)
	c.attachTSIG(m, c.axfrKeyName, c.axfrAlg)

	trs := &dns.Transfer{
		DialTimeout: c.timeout,
		ReadTimeout: c.axfrTO,
		TsigSecret:  c.tsigSecret,
	}
	if deadline, ok := ctx.Deadline(); ok {
		trs.ReadTimeout = time.Until(deadline)
	}

	env, err := trs.In(m, c.tcpAddr)
	if err != nil {
		metrics.IncZoneTransfersFailed("ixfr", zone)
		logging.LogInternalEvent(c.log, "ixfr_failed", slog.LevelError,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		return IXFRResult{}, &ZoneTransferError{
			Message: fmt.Sprintf("IXFR transfer failed for %s", zone),
			Cause:   err,
		}
	}

	var msgs []*dns.Msg
	for e := range env {
		if e.Error != nil {
			metrics.IncZoneTransfersFailed("ixfr", zone)
			return IXFRResult{}, &ZoneTransferError{
				Message: fmt.Sprintf("IXFR transfer failed for %s", zone),
				Cause:   e.Error,
			}
		}
		msg := new(dns.Msg)
		msg.Answer = e.RR
		msgs = append(msgs, msg)
	}

	result, err := ParseIXFRResponse(msgs, fromSerial)
	if err != nil {
		metrics.IncZoneTransfersFailed("ixfr", zone)
		return IXFRResult{}, err
	}
	if result.IsFullAXFR {
		logging.LogInternalEvent(c.log, "ixfr_fallback_axfr", slog.LevelInfo,
			slog.String("zone", zone),
			slog.Uint64("serial", uint64(result.NewSerial)),
			slog.Int("record_count", len(result.Adds)),
		)
	} else {
		metrics.IncZoneTransfers("ixfr", zone)
		logging.LogInternalEvent(c.log, "ixfr_complete", slog.LevelInfo,
			slog.String("zone", zone),
			slog.Uint64("current_serial", uint64(result.NewSerial)),
			slog.Int("operations", len(result.Operations)),
		)
	}
	return result, nil
}

func (c *Client) attachTSIG(m *dns.Msg, keyName, alg string) {
	if keyName == "" || len(c.tsigSecret) == 0 {
		return
	}
	m.SetTsig(keyName, alg, 300, time.Now().Unix())
}

// SendUpdate sends a prepared UPDATE message via the TCP pool with TSIG.
func (c *Client) SendUpdate(ctx context.Context, zone string, msg *dns.Msg) (*dns.Msg, error) {
	zone = NormalizeZoneName(zone)
	c.attachTSIG(msg, c.updateKeyName, c.updateAlg)

	resp, err := c.pool.Exchange(ctx, msg, c.tsigSecret)
	if c.Hooks != nil {
		c.Hooks.OnUpdateResult(ctx, zone, msg, resp, err)
	}
	if err != nil {
		metrics.IncDDNSUpdatesFailed("transport")
		logging.LogInternalEvent(c.log, "ddns_update_error", slog.LevelError,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		return nil, &UpdateError{Message: "update failed", Cause: err}
	}

	rcode := resp.Rcode
	if rcode == dns.RcodeSuccess {
		metrics.IncDDNSUpdatesSuccessful()
		logging.LogInternalEvent(c.log, "ddns_update_success", slog.LevelInfo,
			slog.String("zone", zone),
			slog.String("server", c.serverIP),
		)
		return resp, nil
	}

	rcodeText := dns.RcodeToString[rcode]
	switch rcode {
	case dns.RcodeNXRrset, dns.RcodeYXRrset, dns.RcodeNameError, dns.RcodeYXDomain:
		metrics.IncDDNSUpdatesFailed("prerequisite")
		logging.LogInternalEvent(c.log, "ddns_prereq_failed", slog.LevelWarn,
			slog.String("zone", zone),
			slog.String("rcode", rcodeText),
		)
		return resp, &PrerequisiteError{
			Message:   fmt.Sprintf("Prerequisite failed: %s - DNS state may have changed", rcodeText),
			Rcode:     rcode,
			RcodeText: rcodeText,
		}
	default:
		metrics.IncDDNSUpdatesFailed("rcode")
		logging.LogInternalEvent(c.log, "ddns_update_failed", slog.LevelError,
			slog.String("zone", zone),
			slog.String("rcode", rcodeText),
		)
		return resp, &UpdateError{
			Message: fmt.Sprintf("Update failed with rcode %s", rcodeText),
			Rcode:   rcode,
		}
	}
}

// PrepareAdd builds an ADD UPDATE matching Python single-op semantics.
// CNAME uses NXDOMAIN (NameNotUsed); other types use NXRRSET for the type
// plus absent CNAME at the same name.
func (c *Client) PrepareAdd(zone, name string, ttl uint32, rdtype, rdclass string, records []string, prereqNotExists bool) (*dns.Msg, error) {
	zone = NormalizeZoneName(zone)
	fqdn, err := RequireNameInZone(name, zone)
	if err != nil {
		return nil, err
	}
	typ, err := TypeFromString(rdtype)
	if err != nil {
		return nil, err
	}
	class, err := ClassFromString(rdclass)
	if err != nil {
		return nil, err
	}

	m := new(dns.Msg)
	m.SetUpdate(zone)

	if prereqNotExists {
		if typ == dns.TypeCNAME {
			m.NameNotUsed([]dns.RR{prereqRR(fqdn, dns.TypeANY)})
		} else {
			m.RRsetNotUsed([]dns.RR{prereqRR(fqdn, typ)})
			m.RRsetNotUsed([]dns.RR{prereqRR(fqdn, dns.TypeCNAME)})
		}
	}

	var inserts []dns.RR
	for _, text := range records {
		rr, err := ParseRdata(typ, class, text)
		if err != nil {
			return nil, err
		}
		rr.Header().Name = fqdn
		rr.Header().Ttl = ttl
		rr.Header().Class = class
		inserts = append(inserts, rr)
	}
	m.Insert(inserts)
	return m, nil
}

// PrepareDelete builds a DELETE UPDATE with YXRRSET prerequisites.
func (c *Client) PrepareDelete(zone, name, rdtype, rdclass string, records, prereqRecords []string) (*dns.Msg, error) {
	zone = NormalizeZoneName(zone)
	fqdn, err := RequireNameInZone(name, zone)
	if err != nil {
		return nil, err
	}
	typ, err := TypeFromString(rdtype)
	if err != nil {
		return nil, err
	}
	class, err := ClassFromString(rdclass)
	if err != nil {
		return nil, err
	}

	m := new(dns.Msg)
	m.SetUpdate(zone)

	if len(prereqRecords) > 0 {
		var used []dns.RR
		for _, text := range prereqRecords {
			rr, err := ParseRdata(typ, class, text)
			if err != nil {
				return nil, err
			}
			rr.Header().Name = fqdn
			rr.Header().Class = class
			used = append(used, rr)
		}
		m.RRsetUsed(used)
	} else {
		m.RRsetUsed([]dns.RR{prereqRR(fqdn, typ)})
	}

	if len(records) > 0 {
		var dels []dns.RR
		for _, text := range records {
			rr, err := ParseRdata(typ, class, text)
			if err != nil {
				return nil, err
			}
			rr.Header().Name = fqdn
			rr.Header().Class = dns.ClassNONE
			rr.Header().Ttl = 0
			dels = append(dels, rr)
		}
		m.Remove(dels)
	} else {
		m.RemoveRRset([]dns.RR{prereqRR(fqdn, typ)})
	}
	return m, nil
}

// PrepareReplace builds a REPLACE UPDATE (delete RRset + insert new records).
func (c *Client) PrepareReplace(zone, name string, ttl uint32, rdtype, rdclass string, newRecords, prereqRecords []string) (*dns.Msg, error) {
	zone = NormalizeZoneName(zone)
	fqdn, err := RequireNameInZone(name, zone)
	if err != nil {
		return nil, err
	}
	typ, err := TypeFromString(rdtype)
	if err != nil {
		return nil, err
	}
	class, err := ClassFromString(rdclass)
	if err != nil {
		return nil, err
	}

	m := new(dns.Msg)
	m.SetUpdate(zone)

	if len(prereqRecords) > 0 {
		var used []dns.RR
		for _, text := range prereqRecords {
			rr, err := ParseRdata(typ, class, text)
			if err != nil {
				return nil, err
			}
			rr.Header().Name = fqdn
			rr.Header().Class = class
			used = append(used, rr)
		}
		m.RRsetUsed(used)
	} else {
		m.RRsetUsed([]dns.RR{prereqRR(fqdn, typ)})
	}

	m.RemoveRRset([]dns.RR{prereqRR(fqdn, typ)})

	var inserts []dns.RR
	for _, text := range newRecords {
		rr, err := ParseRdata(typ, class, text)
		if err != nil {
			return nil, err
		}
		rr.Header().Name = fqdn
		rr.Header().Ttl = ttl
		rr.Header().Class = class
		inserts = append(inserts, rr)
	}
	m.Insert(inserts)
	return m, nil
}

func prereqRR(name string, typ uint16) dns.RR {
	return &dns.RFC3597{Hdr: dns.RR_Header{Name: name, Rrtype: typ, Class: dns.ClassANY, Ttl: 0}}
}

// NormalizeName is a convenience wrapper around RequireNameInZone.
func (c *Client) NormalizeName(name, zone string) (string, error) {
	return RequireNameInZone(name, zone)
}

// RcodeDescription returns a user-friendly description for common UPDATE rcodes.
func RcodeDescription(rcodeText string) string {
	switch strings.ToUpper(rcodeText) {
	case "YXRRSET":
		return "Record already exists when it shouldn't (or a CNAME blocks this type)"
	case "NXRRSET":
		return "Record doesn't exist when it should"
	case "YXDOMAIN":
		return "Name already exists when it shouldn't (CNAME requires an unused name)"
	case "NXDOMAIN":
		return "Name doesn't exist when it should"
	default:
		return ""
	}
}
