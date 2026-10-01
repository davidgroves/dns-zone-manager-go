package dnsx

import (
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2030, 6, 15, 12, 30, 0, 0, time.UTC)

func TestNSUpdateTextToCreatesGroupAdds(t *testing.T) {
	text := `
zone example.com.
update add lb.example.com. 300 A 192.0.2.10
update add lb.example.com. 300 A 192.0.2.11
send
`
	creates, err := NSUpdateTextToCreates(text, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(creates) != 1 || len(creates[0].Operations) != 1 {
		t.Fatalf("%+v", creates)
	}
	op := creates[0].Operations[0]
	if op.Action != "add" || len(op.Records) != 2 {
		t.Fatalf("%+v", op)
	}
	if op.Records[0] != "192.0.2.10" || op.Records[1] != "192.0.2.11" {
		t.Fatalf("%v", op.Records)
	}
}

func TestNSUpdateTextToCreatesNoGroupDifferent(t *testing.T) {
	text := `
zone example.com.
update add a.example.com. 300 A 192.0.2.1
update add b.example.com. 300 A 192.0.2.2
update add a.example.com. 300 A 192.0.2.3
send
`
	creates, err := NSUpdateTextToCreates(text, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(creates[0].Operations) != 3 {
		t.Fatalf("%d ops", len(creates[0].Operations))
	}
}

func TestNSUpdateTextToCreatesPrereqs(t *testing.T) {
	text := `
zone example.com.
prereq nxdomain new.example.com.
prereq yxrrset www.example.com. A
update add new.example.com. 3600 A 192.0.2.100
send
`
	creates, err := NSUpdateTextToCreates(text, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	c := creates[0]
	if c.AutoPrerequisites {
		t.Fatal("expected auto_prerequisites false")
	}
	if len(c.Prerequisites) != 2 {
		t.Fatalf("%+v", c.Prerequisites)
	}

	text = `
zone example.com.
update add new.example.com. 3600 A 192.0.2.100
send
`
	creates, err = NSUpdateTextToCreates(text, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !creates[0].AutoPrerequisites {
		t.Fatal("expected auto true")
	}
}

func TestNSUpdateTextToCreatesMultiAndDelete(t *testing.T) {
	text := `
zone example.com.
update add a.example.com. 300 A 192.0.2.1
send
update add b.example.com. 300 A 192.0.2.2
send
`
	creates, err := NSUpdateTextToCreates(text, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(creates) != 2 {
		t.Fatalf("%d", len(creates))
	}
	if !strings.HasSuffix(creates[0].Name, "· 1") || !strings.HasSuffix(creates[1].Name, "· 2") {
		t.Fatalf("%q %q", creates[0].Name, creates[1].Name)
	}
	if !strings.Contains(creates[0].Name, "NSUPDATE · example.com · 20300615T123000Z") {
		t.Fatalf("name %q", creates[0].Name)
	}

	_, err = NSUpdateTextToCreates("zone example.com.\nupdate delete old.example.com.\nsend", "", fixedNow)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "record type") {
		t.Fatalf("%v", err)
	}

	creates, err = NSUpdateTextToCreates("zone example.com.\nupdate delete old.example.com. A 192.0.2.99\nsend", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	op := creates[0].Operations[0]
	if op.Action != "delete" || op.Records[0] != "192.0.2.99" {
		t.Fatalf("%+v", op)
	}
}

func TestNSUpdateTextToCreatesErrors(t *testing.T) {
	_, err := NSUpdateTextToCreates("; just a comment\n", "", fixedNow)
	if err == nil || !strings.Contains(err.Error(), "No update transactions") {
		t.Fatalf("%v", err)
	}
	_, err = NSUpdateTextToCreates("zone example.com.\nprereq nxdomain new.example.com.\nsend", "", fixedNow)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "no update operations") {
		t.Fatalf("%v", err)
	}

	creates, err := NSUpdateTextToCreates(`
zone example.com.
update add host.example.com. 3600 A 192.0.2.1
send
`, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(creates[0].Description, "update add host.example.com.") {
		t.Fatalf("%q", creates[0].Description)
	}

	draft, err := ParsedUpdateToCreate(ParsedUpdate{
		Zone: "example.com.",
		Operations: []ParsedOperation{
			{Action: "add", Name: "www", TTL: uint32Ptr(300), RdType: "A", Data: "192.0.2.1", Class: "IN"},
		},
	}, "Custom draft name", 0, 0, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "Custom draft name" {
		t.Fatalf("%q", draft.Name)
	}
}

func uint32Ptr(v uint32) *uint32 { return &v }
