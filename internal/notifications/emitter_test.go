package notifications_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scheduler.db")
	s := store.New(config.DatabaseSettings{
		Backend:     "sqlite",
		AutoMigrate: true,
		Path:        path,
	}, time.Hour)
	ctx := context.Background()
	if err := s.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleUpdateMsg(t *testing.T) *dns.Msg {
	t.Helper()
	msg := new(dns.Msg)
	msg.SetUpdate("example.com.")
	rr, err := dns.NewRR("www.example.com. 300 IN A 192.0.2.1")
	if err != nil {
		t.Fatalf("NewRR: %v", err)
	}
	msg.Insert([]dns.RR{rr})
	return msg
}

func TestAutorecordWithoutWebhooks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	emitter := &notifications.EventEmitter{
		Store: s,
		Settings: config.WebhookSettings{
			Enabled:                 false,
			AutorecordManualChanges: true,
		},
	}

	emitter.OnUpdateResult(ctx, "example.com.", sampleUpdateMsg(t), nil, nil)

	events, total, err := s.ListEvents(ctx, store.ListEventsOpts{Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (created+applied)", total)
	}
	got := map[string]bool{}
	for _, ev := range events {
		got[ev.Event] = true
		if ev.Zone != "example.com." {
			t.Fatalf("zone = %q", ev.Zone)
		}
	}
	if !got["created"] || !got["applied"] {
		t.Fatalf("events = %+v, want created and applied", events)
	}

	batch, err := s.OutboxClaimBatch(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("OutboxClaimBatch: %v", err)
	}
	if len(batch) != 0 {
		t.Fatalf("outbox jobs = %d, want 0 when webhooks disabled", len(batch))
	}
}

func TestNoAutorecordWhenDisabled(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	emitter := &notifications.EventEmitter{
		Store: s,
		Settings: config.WebhookSettings{
			Enabled:                 false,
			AutorecordManualChanges: false,
		},
	}

	emitter.OnUpdateResult(ctx, "example.com.", sampleUpdateMsg(t), nil, nil)

	_, total, err := s.ListEvents(ctx, store.ListEventsOpts{Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
}

func TestWebhooksEnqueueStillRequiresEnabled(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	emitter := &notifications.EventEmitter{
		Store: s,
		Settings: config.WebhookSettings{
			Enabled:                 true,
			AutorecordManualChanges: true,
			Events:                  []string{notifications.EventChangeApplied, notifications.EventChangeFailed},
			Targets: []config.WebhookTarget{{
				Name:   "sink",
				Format: "generic",
			}},
		},
	}

	emitter.OnUpdateResult(ctx, "example.com.", sampleUpdateMsg(t), nil, nil)

	batch, err := s.OutboxClaimBatch(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("OutboxClaimBatch: %v", err)
	}
	if len(batch) != 1 {
		t.Fatalf("outbox jobs = %d, want 1", len(batch))
	}
	if batch[0].Target != "sink" {
		t.Fatalf("target = %q", batch[0].Target)
	}
}
