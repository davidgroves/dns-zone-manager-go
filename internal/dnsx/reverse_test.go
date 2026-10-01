package dnsx

import (
	"net"
	"testing"
)

func TestReverseNameFromIP(t *testing.T) {
	got, err := ReverseNameFromIP("192.0.2.100")
	if err != nil {
		t.Fatal(err)
	}
	if got != "100.2.0.192.in-addr.arpa." {
		t.Fatalf("%q", got)
	}

	got, err = ReverseNameFromIP("2001:db8::50")
	if err != nil {
		t.Fatal(err)
	}
	if !stringsHasSuffix(got, ".ip6.arpa.") || !stringsHasPrefix(got, "0.5.0.0.") {
		t.Fatalf("%q", got)
	}

	if _, err := ReverseNameFromIP("not.an.ip"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWalkParents(t *testing.T) {
	ptr, _ := ReverseNameFromIP("192.0.2.100")
	managed := map[string]struct{}{
		"2.0.192.in-addr.arpa.": {},
		"0.192.in-addr.arpa.":   {},
	}
	if got := WalkParents(ptr, managed); got != "2.0.192.in-addr.arpa." {
		t.Fatalf("%q", got)
	}
	managed = map[string]struct{}{"0.192.in-addr.arpa.": {}}
	if got := WalkParents(ptr, managed); got != "0.192.in-addr.arpa." {
		t.Fatalf("%q", got)
	}
	if got := WalkParents(ptr, map[string]struct{}{"example.com.": {}}); got != "" {
		t.Fatalf("%q", got)
	}

	ptr6, _ := ReverseNameFromIP("2001:db8::1")
	managed6 := map[string]struct{}{"8.b.d.0.1.0.0.2.ip6.arpa.": {}}
	if got := WalkParents(ptr6, managed6); got != "8.b.d.0.1.0.0.2.ip6.arpa." {
		t.Fatalf("%q", got)
	}
}

func TestRelativePTRLabel(t *testing.T) {
	ptr, _ := ReverseNameFromIP("192.0.2.100")
	got, err := RelativePTRLabel(ptr, "2.0.192.in-addr.arpa.")
	if err != nil || got != "100" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = RelativePTRLabel(ptr, "0.192.in-addr.arpa.")
	if err != nil || got != "100.2" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = RelativePTRLabel(ptr, "2.0.192.in-addr.arpa")
	if err != nil || got != "100" {
		t.Fatalf("no trailing dot: %q %v", got, err)
	}
}

func TestIsReverseZone(t *testing.T) {
	if !IsReverseZone("2.0.192.in-addr.arpa.") {
		t.Fatal("ipv4")
	}
	if !IsReverseZone("8.b.d.0.1.0.0.2.ip6.arpa.") {
		t.Fatal("ipv6")
	}
	if IsReverseZone("example.com.") {
		t.Fatal("forward")
	}
}

func TestIPFromReverseNameRoundtrip(t *testing.T) {
	for _, ip := range []string{"192.0.2.100", "2001:db8::1", "127.0.0.1", "::1"} {
		ptr, err := ReverseNameFromIP(ip)
		if err != nil {
			t.Fatal(err)
		}
		got, err := IPFromReverseName(ptr)
		if err != nil {
			t.Fatal(err)
		}
		if net.ParseIP(got).String() != net.ParseIP(ip).String() {
			t.Fatalf("%s -> %s -> %s", ip, ptr, got)
		}
	}
}

func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
