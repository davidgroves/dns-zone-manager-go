package dnsx

import (
	"fmt"

	"github.com/miekg/dns"
)

// IXFRResult is the parsed outcome of an IXFR (or AXFR-shaped IXFR fallback).
// Operations reuse the shared Operation type from update_builder.go.
type IXFRResult struct {
	IsFullAXFR bool
	Deletes    []dns.RR
	Adds       []dns.RR
	NewSerial  uint32
	Operations []Operation
}

type indexedRR struct {
	rr  dns.RR
	idx int
}

// ParseIXFRResponse parses IXFR answer sections per RFC 1995.
//
// Format:
//   - SOA (current) — start marker
//   - For each delta: SOA(old) + deletes + SOA(new) + adds
//   - SOA (current) — end marker
//
// Two identical SOAs means the server fell back to a full AXFR.
func ParseIXFRResponse(msgs []*dns.Msg, fromSerial uint32) (IXFRResult, error) {
	var all []dns.RR
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		for _, rr := range msg.Answer {
			if rr == nil || rr.Header().Rrtype == dns.TypeTSIG {
				continue
			}
			all = append(all, rr)
		}
	}

	var soas []indexedRR
	for i, rr := range all {
		if rr.Header().Rrtype == dns.TypeSOA {
			soas = append(soas, indexedRR{rr: rr, idx: i})
		}
	}
	if len(soas) == 0 {
		return IXFRResult{}, &ZoneTransferError{Message: "IXFR response contains no SOA records"}
	}

	firstSOA, ok := soas[0].rr.(*dns.SOA)
	if !ok {
		return IXFRResult{}, &ZoneTransferError{Message: "IXFR first SOA has unexpected type"}
	}
	currentSerial := firstSOA.Serial

	// AXFR-shaped: exactly two SOAs with the same serial.
	if len(soas) == 2 {
		second, ok := soas[1].rr.(*dns.SOA)
		if ok && second.Serial == currentSerial {
			var adds []dns.RR
			var ops []Operation
			for _, rr := range all {
				if rr.Header().Rrtype == dns.TypeSOA {
					continue
				}
				adds = append(adds, dns.Copy(rr))
				ops = append(ops, operationFromRR("add", rr))
			}
			return IXFRResult{
				IsFullAXFR: true,
				Adds:       adds,
				NewSerial:  currentSerial,
				Operations: ops,
			}, nil
		}
	}

	// True IXFR: skip first and last SOA markers; process old/new pairs.
	var deletes, adds []dns.RR
	var ops []Operation

	i := 1
	for i < len(soas)-1 {
		oldSOA, ok1 := soas[i].rr.(*dns.SOA)
		if !ok1 {
			return IXFRResult{}, &ZoneTransferError{Message: "IXFR old SOA has unexpected type"}
		}
		if i+1 >= len(soas) {
			break
		}
		newSOA, ok2 := soas[i+1].rr.(*dns.SOA)
		if !ok2 {
			return IXFRResult{}, &ZoneTransferError{Message: "IXFR new SOA has unexpected type"}
		}

		oldIdx := soas[i].idx
		newIdx := soas[i+1].idx

		// Deletions between old SOA and new SOA.
		for j := oldIdx + 1; j < newIdx; j++ {
			rr := all[j]
			if rr.Header().Rrtype == dns.TypeSOA {
				continue
			}
			deletes = append(deletes, dns.Copy(rr))
			ops = append(ops, operationFromRR("delete", rr))
		}

		// Additions between new SOA and the next SOA (or end).
		nextIdx := len(all)
		if i+2 < len(soas) {
			nextIdx = soas[i+2].idx
		}
		for j := newIdx + 1; j < nextIdx; j++ {
			rr := all[j]
			if rr.Header().Rrtype == dns.TypeSOA {
				continue
			}
			adds = append(adds, dns.Copy(rr))
			ops = append(ops, operationFromRR("add", rr))
		}

		_ = oldSOA
		_ = newSOA
		i += 2
	}

	// Eligibility: fromSerial must be less than (or equal, meaning no-op path)
	// the returned current serial in RFC 1982 order when an incremental result
	// is claimed. Equal is fine; greater means the response is inconsistent.
	if fromSerial != 0 && !Less(fromSerial, currentSerial) && fromSerial != currentSerial {
		return IXFRResult{}, &ZoneTransferError{
			Message: fmt.Sprintf(
				"IXFR serial eligibility failed: from=%d current=%d",
				fromSerial, currentSerial,
			),
		}
	}

	return IXFRResult{
		IsFullAXFR: false,
		Deletes:    deletes,
		Adds:       adds,
		NewSerial:  currentSerial,
		Operations: ops,
	}, nil
}

func operationFromRR(action string, rr dns.RR) Operation {
	h := rr.Header()
	ttl := h.Ttl
	if action == "delete" {
		ttl = 0
	}
	return Operation{
		Action:  action,
		Name:    dns.Fqdn(h.Name),
		Type:    TypeName(h.Rrtype),
		Class:   ClassName(h.Class),
		TTL:     ttl,
		Records: []string{RdataText(rr)},
	}
}
