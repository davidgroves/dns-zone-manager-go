package dnsx

import (
	"encoding/json"
	"testing"

	"github.com/miekg/dns"
)

func TestParseIXFRResponseFullAXFR(t *testing.T) {
	soa1 := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 100 7200 3600 1209600 3600")
	a := mustRR(t, "www.example.com. 300 IN A 192.0.2.1")
	ns := mustRR(t, "example.com. 3600 IN NS ns.example.com.")
	soa2 := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 100 7200 3600 1209600 3600")

	msg := new(dns.Msg)
	msg.Answer = []dns.RR{soa1, a, ns, soa2}

	res, err := ParseIXFRResponse([]*dns.Msg{msg}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsFullAXFR {
		t.Fatal("expected IsFullAXFR")
	}
	if res.NewSerial != 100 {
		t.Fatalf("serial=%d", res.NewSerial)
	}
	if len(res.Adds) != 2 {
		t.Fatalf("adds=%d want 2", len(res.Adds))
	}
	if len(res.Deletes) != 0 {
		t.Fatalf("deletes=%d want 0", len(res.Deletes))
	}
}

func TestParseIXFRResponseIncremental(t *testing.T) {
	// current SOA 200
	soaCur := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 200 7200 3600 1209600 3600")
	// old SOA 100 — start deletes
	soaOld := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 100 7200 3600 1209600 3600")
	delA := mustRR(t, "old.example.com. 300 IN A 192.0.2.10")
	// new SOA 200 — start adds
	soaNew := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 200 7200 3600 1209600 3600")
	addA := mustRR(t, "new.example.com. 300 IN A 192.0.2.20")
	// final SOA 200
	soaEnd := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 200 7200 3600 1209600 3600")

	msg := new(dns.Msg)
	msg.Answer = []dns.RR{soaCur, soaOld, delA, soaNew, addA, soaEnd}

	res, err := ParseIXFRResponse([]*dns.Msg{msg}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsFullAXFR {
		t.Fatal("unexpected full AXFR")
	}
	if res.NewSerial != 200 {
		t.Fatalf("serial=%d", res.NewSerial)
	}
	if len(res.Deletes) != 1 || len(res.Adds) != 1 {
		t.Fatalf("deletes=%d adds=%d", len(res.Deletes), len(res.Adds))
	}
	if len(res.Operations) != 2 {
		t.Fatalf("ops=%d", len(res.Operations))
	}
	if res.Operations[0].Action != "delete" || res.Operations[1].Action != "add" {
		t.Fatalf("ops order: %+v", res.Operations)
	}
}

func TestParseIXFRResponseSerialEligibility(t *testing.T) {
	soa1 := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 50 7200 3600 1209600 3600")
	soaOld := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 40 7200 3600 1209600 3600")
	soaNew := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 50 7200 3600 1209600 3600")
	soa2 := mustRR(t, "example.com. 3600 IN SOA ns.example.com. host.example.com. 50 7200 3600 1209600 3600")
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{soa1, soaOld, soaNew, soa2}

	// fromSerial greater than current in RFC 1982 sense (wrap-aware Less fails)
	_, err := ParseIXFRResponse([]*dns.Msg{msg}, 100)
	if err == nil {
		t.Fatal("expected eligibility error")
	}
}

func TestSerialLessEligibility(t *testing.T) {
	if !Less(100, 200) {
		t.Fatal("100 should be less than 200")
	}
	if Less(200, 100) {
		t.Fatal("200 should not be less than 100")
	}
	// wrap: near end of space
	if !Less(0xffffff00, 10) {
		t.Fatal("wrapped serial should be less")
	}
}

func TestOperationJSONMatchesLivePayload(t *testing.T) {
	raw, err := json.Marshal(Operation{
		Action:  "add",
		Name:    "www.example.com.",
		Type:    "A",
		Class:   "IN",
		TTL:     60,
		Records: []string{"192.0.2.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"action", "name", "type", "rdclass", "ttl", "records"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %q in %s", key, raw)
		}
	}
}

func TestIXFRMsgCarriesSOANames(t *testing.T) {
	m := ixfrMsg("example.com.", 100, &dns.SOA{
		Ns:   "ns1.example.com.",
		Mbox: "admin.example.com.",
	})
	if len(m.Question) != 1 || m.Question[0].Qtype != dns.TypeIXFR {
		t.Fatalf("question=%v", m.Question)
	}
	if len(m.Ns) != 1 {
		t.Fatalf("authority=%d", len(m.Ns))
	}
	soa, ok := m.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("authority type %T", m.Ns[0])
	}
	if soa.Serial != 100 || soa.Ns != "ns1.example.com." || soa.Mbox != "admin.example.com." {
		t.Fatalf("soa serial=%d ns=%q mbox=%q", soa.Serial, soa.Ns, soa.Mbox)
	}
}

func TestAlgorithmFromString(t *testing.T) {
	if AlgorithmFromString("hmac-sha256") != dns.HmacSHA256 {
		t.Fatal("sha256")
	}
	if AlgorithmFromString("unknown") != dns.HmacSHA256 {
		t.Fatal("default")
	}
	if AlgorithmFromString("hmac-sha512") != dns.HmacSHA512 {
		t.Fatal("sha512")
	}
}
