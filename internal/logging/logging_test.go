package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewOTLPHandlerResourceMerge(t *testing.T) {
	// resource.Default() uses the SDK schema URL. Merging an older semconv
	// schema used to fail here and Configure then disabled OTLP export.
	h, err := newOTLPHandler("http://127.0.0.1:1", slog.LevelInfo)
	if err != nil {
		t.Fatalf("newOTLPHandler: %v", err)
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info logs should be enabled")
	}
}
