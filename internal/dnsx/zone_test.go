package dnsx

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func sampleZoneRRs(t *testing.T) []dns.RR {
	t.Helper()
	origin := "example.com."
	rrs := []dns.RR{
		mustRR(t, origin+" 3600 IN SOA ns.example.com. host.example.com. 2024010101 7200 3600 1209600 300"),
		mustRR(t, origin+" 3600 IN NS ns.example.com."),
		mustRR(t, "www.example.com. 300 IN A 192.0.2.1"),
		mustRR(t, "www.example.com. 300 IN A 192.0.2.2"),
		mustRR(t, "mail.example.com. 300 IN MX 10 mail.example.com."),
	}
	return rrs
}

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		t.Fatal(err)
	}
	return rr
}

func TestZoneSetFromAXFR(t *testing.T) {
	z := NewZone("example.com")
	if err := z.SetFromAXFR(sampleZoneRRs(t)); err != nil {
		t.Fatal(err)
	}
	if z.RRsetCount() != 4 {
		t.Fatalf("rrsets %d", z.RRsetCount())
	}
	if z.RecordCount() != 5 {
		t.Fatalf("records %d", z.RecordCount())
	}
	serial, ok := z.SOASerial()
	if !ok || serial != 2024010101 {
		t.Fatalf("serial %d ok=%v", serial, ok)
	}
}

func TestZoneSetFromAXFRStripsTrailingSOA(t *testing.T) {
	z := NewZone("example.com.")
	rrs := sampleZoneRRs(t)
	soa := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 2024010101 7200 3600 1209600 300")
	rrs = append(rrs, soa)
	if err := z.SetFromAXFR(rrs); err != nil {
		t.Fatal(err)
	}
	if z.RecordCount() != 5 {
		t.Fatalf("records %d", z.RecordCount())
	}
	info, ok := z.GetRRset("@", dns.TypeSOA)
	if !ok || len(info.Records) != 1 {
		t.Fatalf("soa records: ok=%v n=%d", ok, len(info.Records))
	}
}

func TestZoneInsertRRDedupesIdenticalRdata(t *testing.T) {
	z := NewZone("example.com.")
	a := mustRR(t, "www.example.com. 300 IN A 192.0.2.1")
	if err := z.SetFromAXFR([]dns.RR{
		mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 1 3600 600 86400 60"),
		a,
		mustRR(t, "www.example.com. 300 IN A 192.0.2.1"),
	}); err != nil {
		t.Fatal(err)
	}
	info, ok := z.GetRRset("www", dns.TypeA)
	if !ok || len(info.Records) != 1 {
		t.Fatalf("A records: ok=%v n=%d", ok, len(info.Records))
	}
}

func TestZoneGetAndList(t *testing.T) {
	z := NewZone("example.com.")
	_ = z.SetFromAXFR(sampleZoneRRs(t))

	info, ok := z.GetRRset("www", dns.TypeA)
	if !ok || len(info.Records) != 2 {
		t.Fatalf("get rrset: ok=%v records=%d", ok, len(info.Records))
	}

	page, total, next, hasMore := z.ListRRsets(0, 2, nil)
	if total != 4 || len(page) != 2 || !hasMore || next == nil {
		t.Fatalf("page1 total=%d len=%d hasMore=%v", total, len(page), hasMore)
	}
	page2, _, _, hasMore2 := z.ListRRsets(0, 2, next)
	if len(page2) != 2 || hasMore2 {
		t.Fatalf("page2 len=%d hasMore=%v", len(page2), hasMore2)
	}
}

func TestZoneSearch(t *testing.T) {
	z := NewZone("example.com.")
	_ = z.SetFromAXFR(sampleZoneRRs(t))
	pat := regexp.MustCompile(`192\.0\.2`)
	items, total, _, _ := z.Search(nil, pat, nil, 0, 10, nil)
	if total != 1 || len(items) != 1 || len(items[0].Records) != 2 {
		t.Fatalf("search: total=%d items=%d", total, len(items))
	}
}

func TestZoneMutations(t *testing.T) {
	z := NewZone("example.com.")
	_ = z.SetFromAXFR(sampleZoneRRs(t))

	if err := z.AddRecords("api", dns.TypeA, dns.ClassINET, 60, []string{"192.0.2.9"}); err != nil {
		t.Fatal(err)
	}
	if err := z.DeleteRecords("www", dns.TypeA, dns.ClassINET, []string{"192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	info, ok := z.GetRRset("www", dns.TypeA)
	if !ok || len(info.Records) != 1 {
		t.Fatalf("after delete: %v", info)
	}
	if err := z.ReplaceRRset("api", dns.TypeA, dns.ClassINET, 120, []string{"192.0.2.10"}); err != nil {
		t.Fatal(err)
	}
	info, ok = z.GetRRset("api", dns.TypeA)
	if !ok || info.TTL != 120 || info.Records[0] != "192.0.2.10" {
		t.Fatalf("replace: %+v", info)
	}
}

func TestZoneIndexInsertSorted(t *testing.T) {
	z := NewZone("example.com.")
	_ = z.SetFromAXFR(sampleZoneRRs(t))

	names := []string{"zeta", "alpha", "mid", "beta", "api"}
	for i, name := range names {
		if err := z.AddRecords(name, dns.TypeA, dns.ClassINET, 60, []string{fmt.Sprintf("192.0.2.%d", i+10)}); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	page, total, _, _ := z.ListRRsets(0, 100, nil)
	if total != 4+len(names) {
		t.Fatalf("total=%d", total)
	}
	prev := ""
	for _, it := range page {
		name := strings.ToLower(it.Name)
		if prev != "" && name < prev {
			t.Fatalf("index not sorted: %q after %q", name, prev)
		}
		if name != prev {
			prev = name
		}
	}
	// Idempotent re-insert path via replace (delete+add).
	if err := z.ReplaceRRset("alpha", dns.TypeA, dns.ClassINET, 60, []string{"192.0.2.99"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := z.GetRRset("alpha", dns.TypeA); !ok {
		t.Fatal("alpha missing after replace")
	}
}

func TestZoneSetSOASerialAndIXFR(t *testing.T) {
	z := NewZone("example.com.")
	_ = z.SetFromAXFR(sampleZoneRRs(t))

	if err := z.SetSOASerial(2024010199); err != nil {
		t.Fatal(err)
	}
	serial, _ := z.SOASerial()
	if serial != 2024010199 {
		t.Fatalf("serial %d", serial)
	}

	del := []dns.RR{mustRR(t, "mail.example.com. 300 IN MX 10 mail.example.com.")}
	add := []dns.RR{mustRR(t, "new.example.com. 300 IN A 192.0.2.50")}
	if err := z.ApplyIXFRChanges(del, add, 2024010200); err != nil {
		t.Fatal(err)
	}
	if _, ok := z.GetRRset("mail", dns.TypeMX); ok {
		t.Fatal("MX should be deleted")
	}
	if _, ok := z.GetRRset("new", dns.TypeA); !ok {
		t.Fatal("A should be added")
	}
	serial, _ = z.SOASerial()
	if serial != 2024010200 {
		t.Fatalf("serial %d", serial)
	}
}

func TestZoneToTextAndTSIGSkipped(t *testing.T) {
	z := NewZone("example.com.")
	rrs := sampleZoneRRs(t)
	rrs = append(rrs, &dns.TSIG{
		Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeTSIG, Class: dns.ClassANY, Ttl: 0},
	})
	if err := z.SetFromAXFR(rrs); err != nil {
		t.Fatal(err)
	}
	text := z.ToText()
	if text == "" || !strings.Contains(text, "$ORIGIN example.com.") || !strings.Contains(text, "www") {
		t.Fatalf("to text: %q", text)
	}
}

func TestTypesAndIDN(t *testing.T) {
	if !IsValidType("HTTPS") {
		t.Fatal("HTTPS valid")
	}
	if IsUpdatableType("TSIG") {
		t.Fatal("TSIG not updatable")
	}
	norm, err := NormalizeType("a")
	if err != nil || norm != "A" {
		t.Fatalf("normalize %q err=%v", norm, err)
	}
	info := GetIDNInfo("xn--fsq.xn--0zwm56d.")
	if !info["has_idn"].(bool) {
		t.Fatal("expected idn")
	}
	utf8Info := GetRecordsUTF8Info([]string{`Chinese: \231\189\145\231\187\156`})
	if !utf8Info.HasUTF8 {
		t.Fatal("expected utf8 escapes")
	}
}
