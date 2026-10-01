package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/davidgroves/dns-zone-manager-go/internal/version"
)

var (
	configureOnce sync.Once
	defaultLogger *slog.Logger
)

// Default returns the configured application logger.
func Default() *slog.Logger {
	if defaultLogger == nil {
		Configure("json", "info", "")
	}
	return defaultLogger
}

// Configure sets up structured logging to stderr and optional OTLP export.
func Configure(format string, level string, otlpEndpoint string) {
	configureOnce.Do(func() {
		lvl := parseLevel(level)
		opts := &slog.HandlerOptions{Level: lvl}

		var stderrHandler slog.Handler
		switch strings.ToLower(format) {
		case "text":
			stderrHandler = slog.NewTextHandler(os.Stderr, opts)
		default:
			stderrHandler = slog.NewJSONHandler(os.Stderr, opts)
		}

		h := stderrHandler
		if otlpEndpoint != "" {
			if otlpHandler, err := newOTLPHandler(otlpEndpoint, lvl); err == nil {
				h = &fanoutHandler{handlers: []slog.Handler{stderrHandler, otlpHandler}}
			} else {
				slog.New(stderrHandler).Warn(
					"OTLP log export disabled",
					slog.String("endpoint", otlpEndpoint),
					slog.String("error", err.Error()),
				)
			}
		}

		defaultLogger = slog.New(h)
		slog.SetDefault(defaultLogger)
	})
}

func parseLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newOTLPHandler(endpoint string, lvl slog.Level) (slog.Handler, error) {
	endpoint = strings.TrimRight(endpoint, "/")
	exporter, err := otlploghttp.New(
		context.Background(),
		otlploghttp.WithEndpointURL(endpoint+"/v1/logs"),
	)
	if err != nil {
		return nil, err
	}

	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "dns-zone-manager"
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceNamespace("dns-zone-manager"),
			semconv.ServiceVersion(version.Current()),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp resource: %w", err)
	}

	provider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(exporter)),
	)

	handler := otelslog.NewHandler(
		serviceName,
		otelslog.WithLoggerProvider(provider),
	)
	return &levelHandler{Handler: handler, level: lvl}, nil
}

type levelHandler struct {
	slog.Handler
	level slog.Level
}

func (h *levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level && h.Handler.Enabled(ctx, level)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r.Clone())
		}
	}
	return nil
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}

// LogInternalEvent logs a structured non-request event.
func LogInternalEvent(logger *slog.Logger, event string, level slog.Level, attrs ...any) {
	if logger == nil {
		logger = Default()
	}
	args := append([]any{
		slog.String("event", event),
		slog.String("service", "dns-api"),
		slog.String("version", version.Current()),
	}, attrs...)
	logger.Log(context.Background(), level, event, args...)
}

// Discard returns an io.Writer that drops all output (for tests).
func Discard() io.Writer {
	return io.Discard
}
