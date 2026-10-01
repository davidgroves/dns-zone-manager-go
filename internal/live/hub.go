package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

// ConnectionLimitError is raised when a subscription would exceed configured caps.
type ConnectionLimitError struct {
	Reason string
}

func (e *ConnectionLimitError) Error() string { return e.Reason }

// NormalizeZoneName normalises a zone name to lowercase with a trailing dot.
func NormalizeZoneName(zone string) string {
	zone = strings.TrimSpace(strings.ToLower(zone))
	if zone != "" && zone != "*" && !strings.HasSuffix(zone, ".") {
		zone += "."
	}
	return zone
}

type subscriber struct {
	zone string // normalized zone or "*"
	ip   string
	ch   chan []byte
	done chan struct{}
}

// Hub fans out applied zone changes to per-zone and all-zones WebSocket clients.
type Hub struct {
	maxConnections      int
	maxConnectionsPerIP int
	sendTimeout         time.Duration
	pingInterval        time.Duration
	channelSize         int

	mu      sync.Mutex
	byZone  map[string]map[*subscriber]struct{}
	all     map[*subscriber]struct{}
	ipCount map[string]int
}

// HubConfig configures a Hub.
type HubConfig struct {
	MaxConnections      int
	MaxConnectionsPerIP int
	SendTimeout         time.Duration
	PingInterval        time.Duration
	ChannelSize         int
}

// NewHub creates a Hub with the given limits.
func NewHub(cfg HubConfig) *Hub {
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 500
	}
	if cfg.MaxConnectionsPerIP <= 0 {
		cfg.MaxConnectionsPerIP = 50
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 5 * time.Second
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.ChannelSize <= 0 {
		cfg.ChannelSize = 16
	}
	return &Hub{
		maxConnections:      cfg.MaxConnections,
		maxConnectionsPerIP: cfg.MaxConnectionsPerIP,
		sendTimeout:         cfg.SendTimeout,
		pingInterval:        cfg.PingInterval,
		channelSize:         cfg.ChannelSize,
		byZone:              make(map[string]map[*subscriber]struct{}),
		all:                 make(map[*subscriber]struct{}),
		ipCount:             make(map[string]int),
	}
}

func (h *Hub) totalSubscribers() int {
	n := len(h.all)
	for _, set := range h.byZone {
		n += len(set)
	}
	return n
}

func (h *Hub) checkLimits(ip string) string {
	if h.totalSubscribers() >= h.maxConnections {
		return "max_connections"
	}
	if ip != "" && h.ipCount[ip] >= h.maxConnectionsPerIP {
		return "max_per_ip"
	}
	return ""
}

// Subscribe registers interest in zone (or "*" for all zones) from clientIP.
// The returned channel receives JSON payloads; call Unsubscribe when done.
// Prefer Accept when you have a websocket.Conn — it manages send/ping loops.
func (h *Hub) Subscribe(zone, clientIP string) (*Subscription, error) {
	zone = NormalizeZoneName(zone)
	h.mu.Lock()
	defer h.mu.Unlock()

	if reason := h.checkLimits(clientIP); reason != "" {
		metrics.IncZoneWSRejected(reason)
		return nil, &ConnectionLimitError{Reason: reason}
	}

	sub := &subscriber{
		zone: zone,
		ip:   clientIP,
		ch:   make(chan []byte, h.channelSize),
		done: make(chan struct{}),
	}
	if zone == "*" || zone == "" {
		sub.zone = "*"
		h.all[sub] = struct{}{}
		logging.LogInternalEvent(nil, "zone_ws_subscribed", slog.LevelInfo,
			slog.String("zone", "*"), slog.String("scope", "all"),
		)
	} else {
		set := h.byZone[zone]
		if set == nil {
			set = make(map[*subscriber]struct{})
			h.byZone[zone] = set
		}
		set[sub] = struct{}{}
		logging.LogInternalEvent(nil, "zone_ws_subscribed", slog.LevelInfo,
			slog.String("zone", zone), slog.String("scope", "zone"),
		)
	}
	if clientIP != "" {
		h.ipCount[clientIP]++
	}
	h.refreshGaugesLocked()
	return &Subscription{hub: h, sub: sub, Events: sub.ch}, nil
}

// Subscription is a live zone-change feed.
type Subscription struct {
	hub    *Hub
	sub    *subscriber
	Events <-chan []byte
}

// Unsubscribe removes the subscription.
func (s *Subscription) Unsubscribe() {
	if s == nil || s.hub == nil || s.sub == nil {
		return
	}
	s.hub.remove(s.sub)
}

// Accept registers the connection, runs send + ping loops until ctx is done
// or the peer closes, then unsubscribes.
func (h *Hub) Accept(ctx context.Context, conn *websocket.Conn, zone, clientIP string) error {
	sub, err := h.Subscribe(zone, clientIP)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	errCh := make(chan error, 2)
	go func() {
		errCh <- h.sendLoop(ctx, conn, sub)
	}()
	go func() {
		errCh <- h.readLoop(ctx, conn)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (h *Hub) sendLoop(ctx context.Context, conn *websocket.Conn, sub *Subscription) error {
	ping := time.NewTicker(h.pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sub.sub.done:
			return nil
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, h.sendTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				metrics.IncZoneWSSendErrors()
				return err
			}
		case payload, ok := <-sub.Events:
			if !ok {
				return nil
			}
			writeCtx, cancel := context.WithTimeout(ctx, h.sendTimeout)
			err := conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				metrics.IncZoneWSSendErrors()
				return err
			}
		}
	}
}

func (h *Hub) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		// App-level text ping -> pong (matches Python protocol).
		if string(data) == "ping" {
			writeCtx, cancel := context.WithTimeout(ctx, h.sendTimeout)
			err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"pong"}`))
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

// Broadcast fans out payload to zone subscribers and all-zones subscribers.
// Slow clients (full channel) are dropped. Never blocks; never raises.
func (h *Hub) Broadcast(zone string, payload map[string]any) {
	zone = NormalizeZoneName(zone)
	if payload == nil {
		payload = map[string]any{}
	}
	payload = cloneMap(payload)
	payload["zone"] = zone

	data, err := json.Marshal(payload)
	if err != nil {
		logging.LogInternalEvent(nil, "zone_ws_broadcast_schedule_failed", slog.LevelWarn,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		return
	}

	h.mu.Lock()
	targets := make([]*subscriber, 0, len(h.all)+16)
	for sub := range h.byZone[zone] {
		targets = append(targets, sub)
	}
	for sub := range h.all {
		targets = append(targets, sub)
	}
	h.mu.Unlock()

	if len(targets) == 0 {
		return
	}
	metrics.IncZoneWSBroadcasts("event")

	var slow []*subscriber
	for _, sub := range targets {
		select {
		case sub.ch <- data:
		default:
			metrics.IncZoneWSSlowClientDrops()
			metrics.IncZoneWSSendErrors()
			slow = append(slow, sub)
		}
	}
	for _, sub := range slow {
		h.remove(sub)
	}
}

func (h *Hub) remove(sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.all, sub)
	if set := h.byZone[sub.zone]; set != nil {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.byZone, sub.zone)
		}
	}
	if sub.ip != "" {
		h.ipCount[sub.ip]--
		if h.ipCount[sub.ip] <= 0 {
			delete(h.ipCount, sub.ip)
		}
	}
	select {
	case <-sub.done:
	default:
		close(sub.done)
	}
	// Drain channel so senders don't block (broadcast uses non-blocking send).
	h.refreshGaugesLocked()
}

func (h *Hub) refreshGaugesLocked() {
	zoneCount := 0
	for _, set := range h.byZone {
		zoneCount += len(set)
	}
	metrics.SetZoneWSSubscribers("zone", float64(zoneCount))
	metrics.SetZoneWSSubscribers("all", float64(len(h.all)))
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// IsConnectionLimit reports whether err is a ConnectionLimitError.
func IsConnectionLimit(err error) bool {
	var cle *ConnectionLimitError
	return errors.As(err, &cle)
}

// LimitReason extracts the rejection reason from a ConnectionLimitError.
func LimitReason(err error) string {
	var cle *ConnectionLimitError
	if errors.As(err, &cle) {
		return cle.Reason
	}
	return fmt.Sprintf("%v", err)
}
