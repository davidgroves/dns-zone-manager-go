package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/version"
)

var sensitiveQueryKey = regexp.MustCompile(`(?i)^(api_key|ticket|token|secret|password)$`)

// WideEvent accumulates request context for one comprehensive log line.
type WideEvent struct {
	RequestID    string
	Method       string
	Path         string
	ClientIP     string
	UserAgent    string
	Query        map[string]string
	User         map[string]any
	DNS          map[string]any
	Status       int
	DurationMS   float64
	Outcome      string
	ErrorType    string
	ErrorMessage string
	ErrorCode    string
	ErrorDetails map[string]any
	Extra        map[string]any

	timestamp time.Time
}

// NewWideEvent creates a wide event with a new request ID.
func NewWideEvent() *WideEvent {
	return &WideEvent{
		RequestID: newRequestID(),
		User:      make(map[string]any),
		DNS:       make(map[string]any),
		Extra:     make(map[string]any),
		timestamp: time.Now().UTC(),
		Outcome:   "success",
	}
}

func newRequestID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "req_" + hex.EncodeToString(b[:])
}

// SetRequest records HTTP request metadata; query values are redacted in place.
func (w *WideEvent) SetRequest(method, path, clientIP, userAgent string, query map[string]string) {
	w.Method = method
	w.Path = path
	w.ClientIP = clientIP
	w.UserAgent = userAgent
	if len(query) > 0 {
		w.Query = RedactQuery(query)
	}
}

// RedactQuery returns a copy of query params with sensitive keys redacted.
func RedactQuery(params map[string]string) map[string]string {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		if sensitiveQueryKey.MatchString(k) {
			out[k] = "[REDACTED]"
		} else {
			out[k] = v
		}
	}
	return out
}

// SetUser records authenticated user context.
func (w *WideEvent) SetUser(userID, authType, name, email string, roles []string) {
	w.User = map[string]any{
		"id":        userID,
		"auth_type": authType,
	}
	if name != "" {
		w.User["name"] = name
	}
	if email != "" {
		w.User["email"] = email
	}
	if len(roles) > 0 {
		w.User["roles"] = roles
	}
}

// SetDNS merges DNS operation fields into the event.
func (w *WideEvent) SetDNS(kv map[string]any) {
	for k, v := range kv {
		w.DNS[k] = v
	}
}

// SetError records error context on the event.
func (w *WideEvent) SetError(errorType, message, code string, details map[string]any) {
	w.ErrorType = errorType
	w.ErrorMessage = message
	w.ErrorCode = code
	w.ErrorDetails = details
}

// SetResponse records response status, timing, and outcome.
func (w *WideEvent) SetResponse(status int, durationMS float64, outcome string) {
	w.Status = status
	w.DurationMS = math.Round(durationMS*100) / 100
	if outcome != "" {
		w.Outcome = outcome
	}
}

// ShouldSample decides tail sampling for successful read requests.
func (w *WideEvent) ShouldSample(sampleRate, slowThresholdMS float64) bool {
	if w.ErrorType != "" || w.ErrorMessage != "" {
		return true
	}
	if w.Status >= 400 {
		return true
	}
	if w.DurationMS > slowThresholdMS {
		return true
	}
	switch strings.ToUpper(w.Method) {
	case "POST", "PUT", "DELETE", "PATCH":
		return true
	}
	return randFloat() < sampleRate
}

func randFloat() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
	return float64(n) / float64(^uint64(0))
}

// Emit writes the wide event as a single structured log record.
func (w *WideEvent) Emit(logger *slog.Logger) {
	if logger == nil {
		logger = Default()
	}

	level := slog.LevelInfo
	if w.ErrorType != "" || w.ErrorMessage != "" || w.Status >= 500 {
		level = slog.LevelError
	} else if w.Status >= 400 {
		level = slog.LevelWarn
	}

	levelName := "INFO"
	switch {
	case level >= slog.LevelError:
		levelName = "ERROR"
	case level >= slog.LevelWarn:
		levelName = "WARN"
	}

	attrs := []any{
		slog.Time("timestamp", w.timestamp),
		slog.String("level", levelName),
		slog.String("event", "request_completed"),
		slog.String("service", "dns-api"),
		slog.String("version", version.Current()),
		slog.Group("request",
			slog.String("id", w.RequestID),
			slog.String("method", w.Method),
			slog.String("path", w.Path),
			slog.String("client_ip", w.ClientIP),
			slog.String("user_agent", w.UserAgent),
			slog.Any("query_params", w.Query),
		),
		slog.Group("response",
			slog.Int("status_code", w.Status),
			slog.Float64("duration_ms", w.DurationMS),
			slog.String("outcome", w.Outcome),
		),
	}

	if len(w.User) > 0 {
		attrs = append(attrs, slog.Any("user", w.User))
	}
	if len(w.DNS) > 0 {
		attrs = append(attrs, slog.Any("dns", w.DNS))
	}
	if w.ErrorType != "" || w.ErrorMessage != "" {
		errAttrs := []any{
			slog.String("type", w.ErrorType),
			slog.String("message", w.ErrorMessage),
		}
		if w.ErrorCode != "" {
			errAttrs = append(errAttrs, slog.String("code", w.ErrorCode))
		}
		if len(w.ErrorDetails) > 0 {
			errAttrs = append(errAttrs, slog.Any("details", w.ErrorDetails))
		}
		attrs = append(attrs, slog.Group("error", errAttrs...))
	}
	for k, v := range w.Extra {
		attrs = append(attrs, slog.Any(k, v))
	}

	logger.Log(context.Background(), level, "request_completed", attrs...)
}
