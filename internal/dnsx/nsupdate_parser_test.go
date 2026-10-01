package dnsx

import (
	"strings"
	"testing"
)

func TestParseNSUpdateBasic(t *testing.T) {
	if got, err := ParseNSUpdate("", ""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	if got, err := ParseNSUpdate("; comment\n# other\n", ""); err != nil || len(got) != 0 {
		t.Fatalf("comments: %v %v", got, err)
	}

	_, err := ParseNSUpdate("update add test 300 A 1.2.3.4\nsend", "")
	if err == nil || !strings.Contains(err.Error(), "No zone specified") {
		t.Fatalf("expected zone error, got %v", err)
	}

	got, err := ParseNSUpdate("update add test 300 A 1.2.3.4\nsend", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Zone != "example.com." {
		t.Fatalf("%+v", got)
	}

	got, err = ParseNSUpdate("zone other.com\nupdate add test 300 A 1.2.3.4\nsend", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Zone != "other.com." {
		t.Fatalf("zone %q", got[0].Zone)
	}
}

func TestParseNSUpdatePrereqs(t *testing.T) {
	cases := []struct {
		line string
		typ  string
		data string
	}{
		{"prereq nxdomain foo.test.com.", "nxdomain", ""},
		{"prereq yxdomain foo.test.com.", "yxdomain", ""},
		{"prereq nxrrset foo.test.com. A", "nxrrset", ""},
		{"prereq nxrrset foo.test.com. IN A", "nxrrset", ""},
		{"prereq yxrrset foo.test.com. A", "yxrrset", ""},
		{"prereq yxrrset foo.test.com. A 1.2.3.4", "yxrrset", "1.2.3.4"},
	}
	for _, tc := range cases {
		got, err := ParseNSUpdate("zone test.com\n"+tc.line+"\nsend", "")
		if err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		p := got[0].Prerequisites[0]
		if p.PrereqType != tc.typ {
			t.Fatalf("%s: type %q", tc.line, p.PrereqType)
		}
		if p.Data != tc.data {
			t.Fatalf("%s: data %q", tc.line, p.Data)
		}
	}
}

func TestParseNSUpdateAddDelete(t *testing.T) {
	got, err := ParseNSUpdate("zone test.com\nupdate add www.test.com. 3600 A 192.0.2.1\nsend", "")
	if err != nil {
		t.Fatal(err)
	}
	op := got[0].Operations[0]
	if op.Action != "add" || op.RdType != "A" || op.Data != "192.0.2.1" || op.TTL == nil || *op.TTL != 3600 {
		t.Fatalf("%+v", op)
	}

	got, err = ParseNSUpdate(`zone test.com
update add test.com. 3600 TXT "v=spf1 -all"
send`, "")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Operations[0].Data != "v=spf1 -all" {
		t.Fatalf("txt data %q", got[0].Operations[0].Data)
	}

	got, err = ParseNSUpdate("zone test.com\nupdate delete www.test.com.\nsend", "")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Operations[0].RdType != "" {
		t.Fatal("expected delete-all")
	}

	got, err = ParseNSUpdate("zone test.com\nupdate delete www.test.com. 3600 A 192.0.2.1\nsend", "")
	if err != nil {
		t.Fatal(err)
	}
	op = got[0].Operations[0]
	if op.RdType != "A" || op.Data != "192.0.2.1" {
		t.Fatalf("%+v", op)
	}
}

func TestParseNSUpdateMultiAndQuit(t *testing.T) {
	text := `
zone test.com
update add a.test.com. 300 A 1.2.3.4
send
update add b.test.com. 300 A 5.6.7.8
send
`
	got, err := ParseNSUpdate(text, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len %d", len(got))
	}

	text = `
zone test.com
update add a.test.com. 300 A 1.2.3.4
send
quit
update add b.test.com. 300 A 5.6.7.8
send
`
	got, err = ParseNSUpdate(text, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("quit should stop: %d", len(got))
	}

	got, err = ParseNSUpdate("zone test.com\nupdate add www.test.com. 300 A 1.2.3.4", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("implicit send: %v %v", got, err)
	}
}

func TestParseNSUpdateIgnoredAndErrors(t *testing.T) {
	text := "server ns1.example.com\nkey mykey secret\nlocal 10.0.0.1\nshow\nanswer\ndebug\nzone test.com\nupdate add a.test.com. 300 A 1.2.3.4\nsend"
	got, err := ParseNSUpdate(text, "")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}

	_, err = ParseNSUpdate("zone test.com\nunknown foo\nsend", "")
	if err == nil || !strings.Contains(err.Error(), "Unknown command") {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), "Line 2") {
		t.Fatalf("line number: %v", err)
	}
}
