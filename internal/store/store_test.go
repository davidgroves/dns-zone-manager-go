package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler.db")
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
	if !s.Ping(ctx) {
		t.Fatal("Ping failed")
	}
	if s.Backend() != "sqlite" {
		t.Fatalf("Backend = %q", s.Backend())
	}
	return s
}

func sampleOp(name string) store.AtomicOperation {
	return store.AtomicOperation{
		Action: "add", Name: name, Type: "A", RDClass: "IN", TTL: 300,
		Records: []string{"192.0.2.1"},
	}
}

func TestCreateGetClaimApply(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	scheduled := now.Add(-time.Minute)

	created, err := s.Create(ctx, store.ChangeCreateData{
		Name: "add-www", Zone: "example.com.",
		Operations:  []store.AtomicOperation{sampleOp("www")},
		ScheduledAt: &scheduled,
		CreatedBy:   strPtr("tester"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != store.StatusScheduled {
		t.Fatalf("status = %q, want scheduled", created.Status)
	}
	if created.NotValidAfter == nil {
		t.Fatal("expected auto not_valid_after")
	}
	if len(created.Operations) != 1 || created.Operations[0].Name != "www" {
		t.Fatalf("operations = %+v", created.Operations)
	}
	if len(created.Events) == 0 || created.Events[0].Event != "created" {
		t.Fatalf("events = %+v", created.Events)
	}

	got, err := s.Get(ctx, created.ID, true)
	if err != nil || got == nil {
		t.Fatalf("Get: %v %#v", err, got)
	}
	if got.Name != "add-www" {
		t.Fatalf("name = %q", got.Name)
	}

	claimed, err := s.ClaimDue(ctx, "worker-1", 2*time.Minute)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if claimed == nil || claimed.ID != created.ID {
		t.Fatalf("claimed = %#v", claimed)
	}
	if claimed.Status != store.StatusRunning {
		t.Fatalf("claimed status = %q", claimed.Status)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("attempts = %d", claimed.Attempts)
	}

	serial := int64(42)
	rcode := "NOERROR"
	applied, err := s.MarkApplied(ctx, claimed.ID, store.MarkAppliedOpts{
		ResultRcode: &rcode, NewSerial: &serial, Actor: strPtr("worker-1"),
	})
	if err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}
	if applied.Status != store.StatusApplied || applied.NewSerial == nil || *applied.NewSerial != 42 {
		t.Fatalf("applied = %#v", applied)
	}

	// Nothing else due
	again, err := s.ClaimDue(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDue empty: %v", err)
	}
	if again != nil {
		t.Fatalf("expected nil claim, got %#v", again)
	}
}

func TestFailAndRetry(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	scheduled := time.Now().UTC().Add(-time.Minute)

	if _, err := s.Create(ctx, store.ChangeCreateData{
		Name: "retry-me", Zone: "example.com.",
		Operations:  []store.AtomicOperation{sampleOp("retry")},
		ScheduledAt: &scheduled,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimed, err := s.ClaimDue(ctx, "w", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimDue: %v %#v", err, claimed)
	}

	failed, err := s.MarkFailed(ctx, claimed.ID, "nxdomain", store.MarkFailedOpts{
		MaxAttempts: 3, RetryBackoff: time.Minute,
	})
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if failed.Status != store.StatusScheduled {
		t.Fatalf("status = %q, want scheduled for retry", failed.Status)
	}
	if failed.NextAttemptAt == nil {
		t.Fatal("expected next_attempt_at")
	}
	if failed.LastError == nil || *failed.LastError != "nxdomain" {
		t.Fatalf("last_error = %#v", failed.LastError)
	}

	// Not due yet because next_attempt_at is in the future
	notYet, err := s.ClaimDue(ctx, "w", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if notYet != nil {
		t.Fatalf("expected no claim before backoff, got %#v", notYet)
	}

	draft, err := s.Create(ctx, store.ChangeCreateData{
		Name: "apply-now-fail", Zone: "example.com.",
		Operations: []store.AtomicOperation{sampleOp("x")},
	})
	if err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	running, err := s.MarkRunning(ctx, draft.ID, "w", time.Minute)
	if err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	if running.Status != store.StatusRunning {
		t.Fatalf("status = %q", running.Status)
	}
	terminal, err := s.MarkFailed(ctx, running.ID, "boom", store.MarkFailedOpts{
		MaxAttempts: 3, RetryBackoff: time.Second,
	})
	if err != nil {
		t.Fatalf("MarkFailed draft: %v", err)
	}
	// No scheduled_at → failed (not retried as scheduled)
	if terminal.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed", terminal.Status)
	}
}

func TestOutbox(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"event": "change_applied", "id": "c1"})

	if err := s.OutboxEnqueue(ctx, "job-1", payload, "slack"); err != nil {
		t.Fatalf("OutboxEnqueue: %v", err)
	}
	now := time.Now().UTC()
	batch, err := s.OutboxClaimBatch(ctx, 10, now)
	if err != nil {
		t.Fatalf("OutboxClaimBatch: %v", err)
	}
	if len(batch) != 1 || batch[0].ID != "job-1" || batch[0].Attempts != 1 {
		t.Fatalf("batch = %#v", batch)
	}

	if err := s.OutboxMarkRetry(ctx, "job-1", "timeout", now.Add(time.Hour)); err != nil {
		t.Fatalf("OutboxMarkRetry: %v", err)
	}
	empty, err := s.OutboxClaimBatch(ctx, 10, now)
	if err != nil {
		t.Fatalf("OutboxClaimBatch: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty after retry deferral, got %#v", empty)
	}

	// Claim again in the future window then deliver
	batch2, err := s.OutboxClaimBatch(ctx, 10, now.Add(2*time.Hour))
	if err != nil || len(batch2) != 1 {
		t.Fatalf("claim after wait: %v %#v", err, batch2)
	}
	if err := s.OutboxMarkDelivered(ctx, "job-1"); err != nil {
		t.Fatalf("OutboxMarkDelivered: %v", err)
	}
	n, err := s.OutboxPurgeDelivered(ctx, now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("OutboxPurgeDelivered: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged = %d", n)
	}
}

func TestRetention(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-48 * time.Hour)
	nva := time.Now().UTC().Add(24 * time.Hour)
	created, err := s.Create(ctx, store.ChangeCreateData{
		Name: "old", Zone: "example.com.",
		Operations:    []store.AtomicOperation{sampleOp("old")},
		ScheduledAt:   &past,
		NotValidAfter: &nva,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimed, err := s.ClaimDue(ctx, "w", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimDue: %v %#v", err, claimed)
	}
	if _, err := s.MarkApplied(ctx, claimed.ID, store.MarkAppliedOpts{}); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}

	// Force updated_at/applied_at older by raw SQL for age purge
	oldISO := store.FormatISO(past)
	if _, err := s.DB().NewRaw(
		`UPDATE scheduled_changes SET applied_at = ?, updated_at = ?, created_at = ? WHERE id = ?`,
		oldISO, oldISO, oldISO, created.ID,
	).Exec(ctx); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	result, err := store.RunRetention(ctx, s, config.RetentionSettings{
		Enabled:    true,
		MaxAgeDays: 1,
		Statuses:   []string{store.StatusApplied, store.StatusFailed, store.StatusCancelled, store.StatusExpired, store.StatusReverted},
		Vacuum:     "off",
	})
	if err != nil {
		t.Fatalf("RunRetention: %v", err)
	}
	if result.PurgedByAge < 1 {
		t.Fatalf("expected age purge, got %+v", result)
	}
	got, err := s.Get(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected purged change, still have %#v", got)
	}
}

func TestCancelAndCountPending(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created, err := s.Create(ctx, store.ChangeCreateData{
		Name: "draft", Zone: "example.com.",
		Operations: []store.AtomicOperation{sampleOp("d")},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "draft-renamed"
	at := time.Now().UTC().Add(time.Hour)
	updated, err := s.Update(ctx, created.ID, store.ChangeUpdateData{
		Name: &newName, ScheduledAt: &at, Actor: strPtr("editor"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != newName || updated.Status != store.StatusScheduled {
		t.Fatalf("updated = %#v", updated)
	}

	n, err := s.CountPending(ctx)
	if err != nil || n < 1 {
		t.Fatalf("CountPending: %v %d", err, n)
	}
	cancelled, err := s.Cancel(ctx, created.ID, strPtr("admin"))
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != store.StatusCancelled {
		t.Fatalf("status = %q", cancelled.Status)
	}
}

func TestJSONTextValueIsString(t *testing.T) {
	j, err := store.MarshalJSON(map[string]any{"zone": "foo.bar."})
	if err != nil {
		t.Fatal(err)
	}
	v, err := j.Value()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Value type %T, want string so Postgres JSON is not bound as bytea", v)
	}
	if s == "" || s[0] != '{' {
		t.Fatalf("Value %q", s)
	}
}

func strPtr(s string) *string { return &s }
