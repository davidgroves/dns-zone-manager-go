package dnsx

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/miekg/dns"
)

// NotifyCallback is invoked when a NOTIFY is accepted (after coalesce).
type NotifyCallback func(zone string)

// NotifyListener listens for RFC 1996 NOTIFY on UDP and TCP.
type NotifyListener struct {
	settings   config.NotifySettings
	tsigKey    *config.TSIGKeyEntry
	onNotify   NotifyCallback
	tsigSecret map[string]string

	udpServer *dns.Server
	tcpServer *dns.Server

	mu          sync.Mutex
	running     bool
	inflight    map[string]chan struct{} // closed when callback loop exits
	pending     map[string]struct{}
	lastStarted map[string]time.Time
	now         func() time.Time
	sleep       func(time.Duration)
}

// NewNotifyListener constructs a NOTIFY listener.
func NewNotifyListener(settings config.NotifySettings, tsigKey *config.TSIGKeyEntry, onNotify NotifyCallback) *NotifyListener {
	l := &NotifyListener{
		settings:    settings,
		tsigKey:     tsigKey,
		onNotify:    onNotify,
		inflight:    make(map[string]chan struct{}),
		pending:     make(map[string]struct{}),
		lastStarted: make(map[string]time.Time),
		now:         time.Now,
		sleep:       time.Sleep,
	}
	if tsigKey != nil && !tsigKey.Secret.IsZero() {
		name := dns.Fqdn(tsigKey.Name)
		l.tsigSecret = map[string]string{name: tsigKey.Secret.String()}
	}
	return l
}

// Start begins UDP and TCP NOTIFY listeners.
func (l *NotifyListener) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return nil
	}
	l.running = true

	addrUDP := net.JoinHostPort(l.settings.BindAddress, fmt.Sprintf("%d", l.settings.UDPPort))
	addrTCP := net.JoinHostPort(l.settings.BindAddress, fmt.Sprintf("%d", l.settings.TCPPort))

	handler := dns.HandlerFunc(l.serveDNS)

	l.udpServer = &dns.Server{
		Addr:       addrUDP,
		Net:        "udp",
		Handler:    handler,
		TsigSecret: l.tsigSecret,
	}
	l.tcpServer = &dns.Server{
		Addr:       addrTCP,
		Net:        "tcp",
		Handler:    handler,
		TsigSecret: l.tsigSecret,
	}

	logging.LogInternalEvent(nil, "notify_udp_started", slog.LevelInfo,
		slog.String("bind_address", l.settings.BindAddress),
		slog.Int("port", l.settings.UDPPort),
	)
	logging.LogInternalEvent(nil, "notify_tcp_started", slog.LevelInfo,
		slog.String("bind_address", l.settings.BindAddress),
		slog.Int("port", l.settings.TCPPort),
	)

	go func() {
		if err := l.udpServer.ListenAndServe(); err != nil {
			logging.LogInternalEvent(nil, "notify_udp_error", slog.LevelError, slog.String("error", err.Error()))
		}
	}()
	go func() {
		if err := l.tcpServer.ListenAndServe(); err != nil {
			logging.LogInternalEvent(nil, "notify_tcp_error", slog.LevelError, slog.String("error", err.Error()))
		}
	}()
	return nil
}

// Stop shuts down the listeners.
func (l *NotifyListener) Stop() error {
	l.mu.Lock()
	l.running = false
	udp, tcp := l.udpServer, l.tcpServer
	l.udpServer, l.tcpServer = nil, nil
	l.mu.Unlock()

	var first error
	if udp != nil {
		if err := udp.Shutdown(); err != nil && first == nil {
			first = err
		}
		logging.LogInternalEvent(nil, "notify_udp_stopped", slog.LevelInfo)
	}
	if tcp != nil {
		if err := tcp.Shutdown(); err != nil && first == nil {
			first = err
		}
		logging.LogInternalEvent(nil, "notify_tcp_stopped", slog.LevelInfo)
	}
	return first
}

func (l *NotifyListener) serveDNS(w dns.ResponseWriter, r *dns.Msg) {
	transport := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		transport = "tcp"
	}
	addr := "unknown"
	if ra := w.RemoteAddr(); ra != nil {
		addr = ra.String()
	}
	resp := l.handleNotifyMsg(r, addr, transport)
	if resp != nil {
		_ = w.WriteMsg(resp)
	}
}

// HandleNotify processes raw DNS wire bytes (useful for tests).
func (l *NotifyListener) HandleNotify(data []byte, source string, transport string) []byte {
	var msg dns.Msg
	if err := msg.Unpack(data); err != nil {
		logging.LogInternalEvent(nil, "notify_parse_error", slog.LevelWarn,
			slog.String("error", err.Error()),
			slog.String("source", source),
		)
		metrics.IncNotifiesRejected(transport, "parse_error")
		return nil
	}
	// When a keyring is configured, verify TSIG on the wire if present.
	if l.tsigSecret != nil && msg.IsTsig() != nil {
		secret, ok := l.tsigSecret[msg.IsTsig().Hdr.Name]
		if !ok || secret == "" {
			logging.LogInternalEvent(nil, "notify_tsig_failed", slog.LevelWarn,
				slog.String("error", "unknown TSIG key"),
				slog.String("source", source),
				slog.String("transport", transport),
			)
			metrics.IncNotifiesRejected(transport, "tsig_failed")
			return nil
		}
		if err := dns.TsigVerify(data, secret, "", false); err != nil {
			logging.LogInternalEvent(nil, "notify_tsig_failed", slog.LevelWarn,
				slog.String("error", err.Error()),
				slog.String("source", source),
				slog.String("transport", transport),
			)
			metrics.IncNotifiesRejected(transport, "tsig_failed")
			return nil
		}
	}
	resp := l.handleNotifyMsg(&msg, source, transport)
	if resp == nil {
		return nil
	}
	out, err := resp.Pack()
	if err != nil {
		return nil
	}
	return out
}

func (l *NotifyListener) handleNotifyMsg(message *dns.Msg, source, transport string) *dns.Msg {
	if message.Opcode != dns.OpcodeNotify {
		logging.LogInternalEvent(nil, "notify_wrong_opcode", slog.LevelDebug,
			slog.String("opcode", dns.OpcodeToString[message.Opcode]),
			slog.String("source", source),
		)
		metrics.IncNotifiesRejected(transport, "wrong_opcode")
		return makeErrorResponse(message, dns.RcodeRefused)
	}
	if len(message.Question) == 0 {
		logging.LogInternalEvent(nil, "notify_no_question", slog.LevelDebug, slog.String("source", source))
		metrics.IncNotifiesRejected(transport, "no_question")
		return makeErrorResponse(message, dns.RcodeFormatError)
	}
	zoneName := message.Question[0].Name

	if l.settings.RequireTSIG {
		if !l.verifyTSIG(message) {
			logging.LogInternalEvent(nil, "notify_tsig_failed", slog.LevelWarn,
				slog.String("zone", zoneName),
				slog.String("source", source),
				slog.String("transport", transport),
			)
			metrics.IncNotifiesRejected(transport, "tsig_failed")
			return makeErrorResponse(message, dns.RcodeNotAuth)
		}
	}

	metrics.IncNotifiesReceived(transport, zoneName)
	logging.LogInternalEvent(nil, "notify_received", slog.LevelInfo,
		slog.String("zone", zoneName),
		slog.String("source", source),
		slog.String("transport", transport),
		slog.Bool("tsig_verified", message.IsTsig() != nil),
	)

	if l.onNotify != nil {
		l.scheduleCallback(zoneName)
	}
	return makeNotifyResponse(message)
}

func (l *NotifyListener) verifyTSIG(message *dns.Msg) bool {
	if message.IsTsig() == nil {
		return false
	}
	if l.tsigSecret == nil {
		return false
	}
	return true
}

func (l *NotifyListener) normalizeZone(zoneName string) string {
	zone := strings.ToLower(strings.TrimSpace(zoneName))
	if zone != "" && !strings.HasSuffix(zone, ".") {
		zone += "."
	}
	return zone
}

func (l *NotifyListener) scheduleCallback(zoneName string) {
	zone := l.normalizeZone(zoneName)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.inflight[zone]; ok {
		l.pending[zone] = struct{}{}
		return
	}
	done := make(chan struct{})
	l.inflight[zone] = done
	go l.coalescedCallback(zone, done)
}

func (l *NotifyListener) coalescedCallback(zone string, done chan struct{}) {
	defer func() {
		l.mu.Lock()
		delete(l.inflight, zone)
		_, again := l.pending[zone]
		if again {
			delete(l.pending, zone)
		}
		close(done)
		l.mu.Unlock()
		if again {
			l.scheduleCallback(zone)
		}
	}()

	for {
		cooldown := time.Duration(l.settings.RefreshCooldownSeconds * float64(time.Second))
		l.mu.Lock()
		last := l.lastStarted[zone]
		now := l.now()
		l.mu.Unlock()
		if cooldown > 0 && !last.IsZero() {
			elapsed := now.Sub(last)
			if elapsed < cooldown {
				l.sleep(cooldown - elapsed)
			}
		}
		l.mu.Lock()
		l.lastStarted[zone] = l.now()
		l.mu.Unlock()

		l.invokeCallback(zone)

		l.mu.Lock()
		_, hasPending := l.pending[zone]
		if hasPending {
			delete(l.pending, zone)
			l.mu.Unlock()
			continue
		}
		l.mu.Unlock()
		return
	}
}

func (l *NotifyListener) invokeCallback(zone string) {
	if l.onNotify == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logging.LogInternalEvent(nil, "notify_callback_error", slog.LevelError,
				slog.String("zone", zone),
				slog.Any("error", r),
			)
		}
	}()
	l.onNotify(zone)
}

func makeNotifyResponse(query *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(query)
	resp.Opcode = dns.OpcodeNotify
	resp.Rcode = dns.RcodeSuccess
	return resp
}

func makeErrorResponse(query *dns.Msg, rcode int) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(query)
	resp.Rcode = rcode
	return resp
}
