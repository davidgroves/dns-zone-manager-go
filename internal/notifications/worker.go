package notifications

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications/formatters"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

var retryableStatuses = map[int]struct{}{
	408: {}, 425: {}, 429: {},
}

// OutboxWorker drains webhook_outbox rows and POSTs formatted payloads.
type OutboxWorker struct {
	Store    *store.Store
	Settings config.WebhookSettings
	Log      *slog.Logger
	HTTP     *http.Client

	pollInterval time.Duration
	batchSize    int

	cancel  context.CancelFunc
	done    chan struct{}
	mu      sync.Mutex
	running bool
}

// NewOutboxWorker creates a worker. Call Start to begin draining.
func NewOutboxWorker(st *store.Store, settings config.WebhookSettings) *OutboxWorker {
	timeout := settings.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &OutboxWorker{
		Store:        st,
		Settings:     settings,
		HTTP:         &http.Client{Timeout: timeout},
		pollInterval: time.Second,
		batchSize:    64,
		done:         make(chan struct{}),
	}
}

// Start begins the background drain loop.
func (w *OutboxWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.running = true
	w.done = make(chan struct{})
	go func() {
		defer close(w.done)
		w.loop(runCtx)
	}()
	logging.LogInternalEvent(w.Log, "webhook_outbox_worker_started", slog.LevelInfo,
		slog.Int("targets", len(w.Settings.Targets)),
	)
}

// Stop cancels the worker and waits for it to exit.
func (w *OutboxWorker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	cancel := w.cancel
	done := w.done
	w.running = false
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-done
	logging.LogInternalEvent(w.Log, "webhook_outbox_worker_stopped", slog.LevelInfo)
}

func (w *OutboxWorker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.drainOnce(ctx); err != nil && ctx.Err() == nil {
			logging.LogInternalEvent(w.Log, "webhook_outbox_drain_error", slog.LevelWarn,
				slog.String("error", err.Error()),
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *OutboxWorker) drainOnce(ctx context.Context) error {
	now := time.Now().UTC()
	batch, err := w.Store.OutboxClaimBatch(ctx, w.batchSize, now)
	if err != nil {
		return err
	}
	metrics.SetWebhookQueueDepth(float64(len(batch)))
	for _, item := range batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.deliver(ctx, item)
	}
	return nil
}

func (w *OutboxWorker) deliver(ctx context.Context, item store.WebhookOutboxItem) {
	target, ok := TargetByName(w.Settings, item.Target)
	if !ok {
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID)
		metrics.IncWebhookDeliveries(item.Target, "unknown_target")
		return
	}

	var event DnsChangeEvent
	if err := json.Unmarshal(item.EventJSON, &event); err != nil {
		metrics.IncWebhookDeliveries(target.Name, "format_error")
		_ = w.Store.OutboxMarkRetry(ctx, item.ID, "invalid event JSON: "+err.Error(), time.Now().UTC().Add(w.retryDelay(item.Attempts)))
		return
	}

	payload, err := formatters.FormatPayload(target.Format, event, w.Settings.BaseURL)
	if err != nil {
		metrics.IncWebhookDeliveries(target.Name, "format_error")
		logging.LogInternalEvent(w.Log, "webhook_delivery_failed", slog.LevelWarn,
			slog.String("target", target.Name),
			slog.String("error", err.Error()),
		)
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID) // non-retryable format error
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		metrics.IncWebhookDeliveries(target.Name, "format_error")
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID)
		return
	}
	// Compact separators matching Python json.dumps(separators=(",", ":"))
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err == nil {
		body = compact.Bytes()
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05") + "+00:00"
	authHeaders, err := BuildAuthHeaders(target, body, timestamp)
	if err != nil {
		metrics.IncWebhookDeliveries(target.Name, "format_error")
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL.String(), bytes.NewReader(body))
	if err != nil {
		w.markRetry(ctx, item, target.Name, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dns-zone-manager-webhook")
	for k, v := range target.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	client := w.clientFor(target)
	started := time.Now()
	resp, err := client.Do(req)
	metrics.ObserveWebhookDelivery(target.Name, time.Since(started).Seconds())
	if err != nil {
		detail := Redact(err.Error(), target)
		logging.LogInternalEvent(w.Log, "webhook_delivery_failed", slog.LevelWarn,
			slog.String("target", target.Name),
			slog.String("outcome", "transport_error"),
			slog.Int("attempt", item.Attempts),
			slog.String("zone", event.Zone),
			slog.String("error", detail),
		)
		w.markRetry(ctx, item, target.Name, detail)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	if resp.StatusCode < 400 {
		metrics.IncWebhookDeliveries(target.Name, "success")
		logging.LogInternalEvent(w.Log, "webhook_delivered", slog.LevelInfo,
			slog.String("target", target.Name),
			slog.String("target_type", target.Format),
			slog.Int("status", resp.StatusCode),
			slog.Int("attempt", item.Attempts),
			slog.String("event", event.Event),
			slog.String("zone", event.Zone),
		)
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID)
		return
	}

	detail := Redact(string(respBody), target)
	retryable := resp.StatusCode >= 500
	if _, ok := retryableStatuses[resp.StatusCode]; ok {
		retryable = true
	}
	if !retryable {
		metrics.IncWebhookDeliveries(target.Name, "http_error")
		logging.LogInternalEvent(w.Log, "webhook_delivery_failed", slog.LevelWarn,
			slog.String("target", target.Name),
			slog.Int("status", resp.StatusCode),
			slog.Int("attempt", item.Attempts),
			slog.Bool("retryable", false),
			slog.String("zone", event.Zone),
			slog.String("error", detail),
		)
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID)
		return
	}

	logging.LogInternalEvent(w.Log, "webhook_delivery_failed", slog.LevelWarn,
		slog.String("target", target.Name),
		slog.String("outcome", "http_error"),
		slog.Int("attempt", item.Attempts),
		slog.String("zone", event.Zone),
		slog.String("error", detail),
	)

	maxAttempts := w.Settings.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if item.Attempts >= maxAttempts {
		metrics.IncWebhookDeliveries(target.Name, "exhausted")
		logging.LogInternalEvent(w.Log, "webhook_delivery_exhausted", slog.LevelError,
			slog.String("target", target.Name),
			slog.Int("attempts", item.Attempts),
			slog.String("zone", event.Zone),
			slog.String("event", event.Event),
		)
		_ = w.Store.OutboxMarkDelivered(ctx, item.ID)
		return
	}
	w.markRetry(ctx, item, target.Name, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail))
}

func (w *OutboxWorker) markRetry(ctx context.Context, item store.WebhookOutboxItem, target, lastErr string) {
	metrics.IncWebhookDeliveries(target, "retry")
	next := time.Now().UTC().Add(w.retryDelay(item.Attempts))
	_ = w.Store.OutboxMarkRetry(ctx, item.ID, lastErr, next)
}

func (w *OutboxWorker) retryDelay(attempts int) time.Duration {
	backoff := w.Settings.RetryBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	if attempts < 1 {
		attempts = 1
	}
	// Exponential: backoff * 2^(attempts-1), matching Python's attempt indexing.
	mult := 1 << (attempts - 1)
	if mult > 64 {
		mult = 64
	}
	return backoff * time.Duration(mult)
}

func (w *OutboxWorker) clientFor(target config.WebhookTarget) *http.Client {
	timeout := w.Settings.Timeout
	if target.TimeoutSeconds != nil && *target.TimeoutSeconds > 0 {
		timeout = time.Duration(*target.TimeoutSeconds * float64(time.Second))
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	insecure := target.AllowInsecure
	switch v := target.VerifyTLS.(type) {
	case bool:
		if !v {
			insecure = true
		}
	case string:
		if v == "false" || v == "0" {
			insecure = true
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
