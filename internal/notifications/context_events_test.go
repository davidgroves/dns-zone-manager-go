package notifications_test

import (
	"context"
	"testing"

	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/miekg/dns"
)

func TestChangeContextOverrideAndRestore(t *testing.T) {
	ctx := context.Background()
	actor := "alice"
	ctx = notifications.WithChangeContext(ctx, notifications.ChangeContext{
		Actor:   &actor,
		Trigger: notifications.TriggerManual,
	})

	changeID := "c1"
	inner := notifications.WithChangeContext(ctx, notifications.ChangeContext{
		Trigger:  notifications.TriggerScheduler,
		ChangeID: &changeID,
	})
	got := notifications.FromContext(inner)
	if got.Actor == nil || *got.Actor != "alice" {
		t.Fatalf("actor=%v", got.Actor)
	}
	if got.Trigger != notifications.TriggerScheduler || got.ChangeID == nil || *got.ChangeID != "c1" {
		t.Fatalf("inner=%+v", got)
	}

	outer := notifications.FromContext(ctx)
	if outer.Trigger != notifications.TriggerManual || outer.ChangeID != nil {
		t.Fatalf("outer=%+v", outer)
	}
}

func TestOperationsFromUpdateAdd(t *testing.T) {
	msg := new(dns.Msg)
	msg.SetUpdate("test.example.")
	rr, err := dns.NewRR("www.test.example. 300 IN A 10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	msg.Insert([]dns.RR{rr})

	ops := notifications.OperationsFromUpdate(msg)
	if len(ops) != 1 {
		t.Fatalf("ops=%v", ops)
	}
	if ops[0].Action != "add" || ops[0].RDType != "A" || ops[0].Records[0] != "10.0.0.1" {
		t.Fatalf("%+v", ops[0])
	}
}

func TestOperationsFromUpdateReplaceCoalesce(t *testing.T) {
	msg := new(dns.Msg)
	msg.SetUpdate("test.example.")
	stub := &dns.A{Hdr: dns.RR_Header{Name: "www.test.example.", Rrtype: dns.TypeA, Class: dns.ClassANY, Ttl: 0}}
	msg.RemoveRRset([]dns.RR{stub})
	rr, _ := dns.NewRR("www.test.example. 60 IN A 10.0.0.2")
	msg.Insert([]dns.RR{rr})

	ops := notifications.OperationsFromUpdate(msg)
	if len(ops) != 1 || ops[0].Action != "replace" {
		t.Fatalf("ops=%+v", ops)
	}
	if ops[0].TTL == nil || *ops[0].TTL != 60 || ops[0].Records[0] != "10.0.0.2" {
		t.Fatalf("%+v", ops[0])
	}
}

func TestOperationsFromUpdateDeleteRRset(t *testing.T) {
	msg := new(dns.Msg)
	msg.SetUpdate("test.example.")
	stub := &dns.TXT{Hdr: dns.RR_Header{Name: "old.test.example.", Rrtype: dns.TypeTXT, Class: dns.ClassANY, Ttl: 0}}
	msg.RemoveRRset([]dns.RR{stub})

	ops := notifications.OperationsFromUpdate(msg)
	if len(ops) != 1 || ops[0].Action != "delete" || ops[0].RDType != "TXT" {
		t.Fatalf("%+v", ops)
	}
	if len(ops[0].Records) != 0 {
		t.Fatalf("records=%v", ops[0].Records)
	}
}
