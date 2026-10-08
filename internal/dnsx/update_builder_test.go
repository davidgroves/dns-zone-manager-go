package dnsx

import (
	"errors"
	"testing"

	"github.com/miekg/dns"
)

func TestBuildUpdateAddAutoPrereq(t *testing.T) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		return 0, nil, false
	}
	typesAt := func(name string) []string { return nil }

	built, err := BuildUpdate(
		"example.com.",
		[]Operation{{Action: "add", Name: "www", Type: "A", TTL: 3600, Records: []string{"192.0.2.1"}}},
		nil,
		true,
		true,
		lookup,
		typesAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.CacheUpdates) != 1 || built.CacheUpdates[0].Action != "add" {
		t.Fatalf("cache updates: %+v", built.CacheUpdates)
	}
	if len(built.AutoPrerequisites) != 2 {
		t.Fatalf("auto prereqs: %+v", built.AutoPrerequisites)
	}
	if built.AutoPrerequisites[0].RdType != "A" || built.AutoPrerequisites[1].RdType != "CNAME" {
		t.Fatalf("prereq types: %+v", built.AutoPrerequisites)
	}
	if built.Msg.Opcode != dns.OpcodeUpdate {
		t.Fatalf("opcode %d", built.Msg.Opcode)
	}
	// Answer section holds prerequisites; Ns holds updates.
	if len(built.Msg.Answer) < 2 {
		t.Fatalf("expected NXRRSET prereqs in Answer, got %d", len(built.Msg.Answer))
	}
}

func TestBuildUpdateAddFailsWhenExists(t *testing.T) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		if typ == "A" {
			return 3600, []string{"192.0.2.1"}, true
		}
		return 0, nil, false
	}
	_, err := BuildUpdate(
		"example.com.",
		[]Operation{{Action: "add", Name: "www", Type: "A", TTL: 3600, Records: []string{"192.0.2.1"}}},
		nil, true, true, lookup, nil,
	)
	var ue *UpdateBuildError
	if !errors.As(err, &ue) || ue.Code != "RRSET_EXISTS" {
		t.Fatalf("got %v", err)
	}
}

func TestBuildUpdateAddWithoutValidateWhenExists(t *testing.T) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		if typ == "A" {
			return 3600, []string{"192.0.2.1"}, true
		}
		return 0, nil, false
	}
	built, err := BuildUpdate(
		"example.com.",
		[]Operation{{Action: "add", Name: "www", Type: "A", TTL: 3600, Records: []string{"192.0.2.1"}}},
		nil, true, false, lookup, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.CacheUpdates) != 1 {
		t.Fatal("expected cache update")
	}
}

func TestBuildUpdateDeleteRequiresExisting(t *testing.T) {
	lookup := func(string, string) (uint32, []string, bool) { return 0, nil, false }
	_, err := BuildUpdate(
		"example.com.",
		[]Operation{{Action: "delete", Name: "www", Type: "A"}},
		nil, true, true, lookup, nil,
	)
	var ue *UpdateBuildError
	if !errors.As(err, &ue) || ue.Code != "RRSET_NOT_FOUND" {
		t.Fatalf("got %v", err)
	}
}

func TestBuildUpdateDeleteYXRRSETWithCachedRdata(t *testing.T) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		if typ == "A" {
			return 300, []string{"192.0.2.1", "192.0.2.2"}, true
		}
		return 0, nil, false
	}
	built, err := BuildUpdate(
		"example.com.",
		[]Operation{{Action: "delete", Name: "www", Type: "A"}},
		nil, true, true, lookup, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Two Used() prereqs in Answer
	if len(built.Msg.Answer) != 2 {
		t.Fatalf("expected 2 YXRRSET rdata prereqs, got %d: %v", len(built.Msg.Answer), built.Msg.Answer)
	}
}

func TestBuildUpdateCNAMEOntoExistingARejected(t *testing.T) {
	lookup := func(string, string) (uint32, []string, bool) { return 0, nil, false }
	typesAt := func(name string) []string {
		if name == "www.example.com." || name == "www" {
			return []string{"A"}
		}
		// RequireNameInZone normalizes to www.example.com.
		return []string{"A"}
	}
	_, err := BuildUpdate(
		"example.com.",
		[]Operation{{Action: "add", Name: "www", Type: "CNAME", TTL: 300, Records: []string{"target.example.com."}}},
		nil, true, true, lookup, typesAt,
	)
	var ue *UpdateBuildError
	if !errors.As(err, &ue) || ue.Code != "CNAME_CONFLICT" {
		t.Fatalf("got %v", err)
	}
}

func TestBuildUpdateDeleteAThenAddCNAMEOK(t *testing.T) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		if typ == "A" {
			return 300, []string{"192.0.2.1"}, true
		}
		return 0, nil, false
	}
	typesAt := func(string) []string { return []string{"A"} }
	built, err := BuildUpdate(
		"example.com.",
		[]Operation{
			{Action: "delete", Name: "www", Type: "A"},
			{Action: "add", Name: "www", Type: "CNAME", TTL: 300, Records: []string{"target.example.com."}},
		},
		nil, true, true, lookup, typesAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.CacheUpdates) != 2 {
		t.Fatalf("cache updates %d", len(built.CacheUpdates))
	}
}

func TestBuildUpdateAddAAndCNAMESameTxnRejected(t *testing.T) {
	lookup := func(string, string) (uint32, []string, bool) { return 0, nil, false }
	_, err := BuildUpdate(
		"example.com.",
		[]Operation{
			{Action: "add", Name: "www", Type: "A", TTL: 300, Records: []string{"192.0.2.1"}},
			{Action: "add", Name: "www", Type: "CNAME", TTL: 300, Records: []string{"target.example.com."}},
		},
		nil, true, true, lookup, nil,
	)
	var ue *UpdateBuildError
	if !errors.As(err, &ue) || ue.Code != "CNAME_CONFLICT" {
		t.Fatalf("got %v", err)
	}
}

func TestBuildUpdateAddSkipsCNAMEPrereqWhenDeletingCNAME(t *testing.T) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		if typ == "CNAME" {
			return 300, []string{"old.example.com."}, true
		}
		return 0, nil, false
	}
	typesAt := func(string) []string { return []string{"CNAME"} }
	built, err := BuildUpdate(
		"example.com.",
		[]Operation{
			{Action: "delete", Name: "www", Type: "CNAME"},
			{Action: "add", Name: "www", Type: "A", TTL: 300, Records: []string{"192.0.2.1"}},
		},
		nil, true, true, lookup, typesAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Only typed NXRRSET for A — no extra CNAME-absent
	cnameAbsent := 0
	for _, p := range built.AutoPrerequisites {
		if p.RdType == "CNAME" && p.PrereqType == "nxrrset" {
			cnameAbsent++
		}
	}
	if cnameAbsent != 0 {
		t.Fatalf("unexpected CNAME nxrrset auto prereqs: %+v", built.AutoPrerequisites)
	}
}

func TestApplyCacheUpdates(t *testing.T) {
	z := NewZone("example.com.")
	err := ApplyCacheUpdates(z, []CacheUpdate{
		{Action: "add", Name: "www", RdType: "A", Class: "IN", TTL: 300, Records: []string{"192.0.2.1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := z.GetRRset("www", dns.TypeA)
	if !ok || len(info.Records) != 1 {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

func TestOperationsFromCacheUpdates(t *testing.T) {
	ops := OperationsFromCacheUpdates([]CacheUpdate{
		{Action: "Replace", Name: "both-1.always-changing.example.", RdType: "a", Class: "", TTL: 60, Records: []string{"203.0.113.1"}},
		{Action: "delete", Name: "gone.example.", RdType: "TXT", Class: "in", Records: []string{"x"}},
	})
	if len(ops) != 2 {
		t.Fatalf("len=%d", len(ops))
	}
	if ops[0].Action != "replace" || ops[0].Type != "A" || ops[0].Class != "IN" || ops[0].TTL != 60 {
		t.Fatalf("op0=%+v", ops[0])
	}
	if ops[1].Action != "delete" || ops[1].Type != "TXT" || ops[1].Class != "IN" {
		t.Fatalf("op1=%+v", ops[1])
	}
}
