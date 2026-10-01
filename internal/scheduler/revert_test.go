package scheduler_test

import (
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/scheduler"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func TestCanRevertRequiresSnapshots(t *testing.T) {
	now := time.Now().UTC()
	change := &store.ScheduledChange{
		Status: store.StatusApplied,
		Operations: []store.ScheduledOperation{
			{Action: "add", Name: "www", Type: "A", RDClass: "IN", TTL: 300, Records: []string{"1.2.3.4"}},
		},
	}
	if scheduler.CanRevert(change) {
		t.Fatal("missing snapshot_at should not revert")
	}
	change.Operations[0].SnapshotAt = &now
	if !scheduler.CanRevert(change) {
		t.Fatal("expected can revert")
	}
}

func TestBuildRevertOpsInverse(t *testing.T) {
	now := time.Now().UTC()
	priorTTL := 600
	change := &store.ScheduledChange{
		Status: store.StatusApplied,
		Operations: []store.ScheduledOperation{
			{
				Action: "add", Name: "a.example.", Type: "A", RDClass: "IN", TTL: 300,
				Records: []string{"10.0.0.1"}, SnapshotAt: &now,
			},
			{
				Action: "delete", Name: "b.example.", Type: "A", RDClass: "IN", TTL: 300,
				PriorTTL: &priorTTL, PriorRecords: []string{"10.0.0.2"}, SnapshotAt: &now,
			},
			{
				Action: "replace", Name: "c.example.", Type: "A", RDClass: "IN", TTL: 300,
				Records: []string{"10.0.0.3"}, PriorTTL: &priorTTL, PriorRecords: []string{"10.0.0.9"}, SnapshotAt: &now,
			},
		},
	}
	ops, err := scheduler.BuildRevertOps(change)
	if err != nil {
		t.Fatal(err)
	}
	// add -> delete; delete -> add; replace -> delete + add
	if len(ops) != 4 {
		t.Fatalf("len=%d ops=%+v", len(ops), ops)
	}
	if ops[0].Action != "delete" || ops[0].Name != "a.example." || ops[0].Records[0] != "10.0.0.1" {
		t.Fatalf("op0=%+v", ops[0])
	}
	if ops[1].Action != "add" || ops[1].TTL != 600 || ops[1].Records[0] != "10.0.0.2" {
		t.Fatalf("op1=%+v", ops[1])
	}
	if ops[2].Action != "delete" || ops[2].Records[0] != "10.0.0.3" {
		t.Fatalf("op2=%+v", ops[2])
	}
	if ops[3].Action != "add" || ops[3].Records[0] != "10.0.0.9" {
		t.Fatalf("op3=%+v", ops[3])
	}
}

func TestBuildRevertOpsAbsentDeleteIsNoop(t *testing.T) {
	now := time.Now().UTC()
	change := &store.ScheduledChange{
		Status: store.StatusApplied,
		Operations: []store.ScheduledOperation{
			{Action: "delete", Name: "gone", Type: "A", SnapshotAt: &now}, // prior_records nil
		},
	}
	ops, err := scheduler.BuildRevertOps(change)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops=%+v", ops)
	}
}
