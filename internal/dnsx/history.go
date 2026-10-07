package dnsx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

// HistoryChange is one add/delete from an IXFR delta batch.
type HistoryChange struct {
	Action  string   `json:"action"`
	Name    string   `json:"name"`
	TTL     uint32   `json:"ttl"`
	Type    string   `json:"type"`
	RDClass string   `json:"rdclass"`
	Records []string `json:"records"`
}

// HistoryBatch is the set of changes between two serials.
type HistoryBatch struct {
	FromSerial uint32          `json:"from_serial"`
	ToSerial   uint32          `json:"to_serial"`
	Changes    []HistoryChange `json:"changes"`
}

// ZoneHistory is the history-shaped IXFR result (RFC 1995 + RFC 1982).
type ZoneHistory struct {
	Zone                string
	CurrentSerial       uint32
	Batches             []HistoryBatch
	IsFullAXFR          bool
	AvailableFromSerial uint32
}

// ParseIXFRHistory parses IXFR messages into per-serial HistoryBatches.
func ParseIXFRHistory(msgs []*dns.Msg, zone string) (ZoneHistory, error) {
	zone = NormalizeZoneName(zone)
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
		return ZoneHistory{}, &ZoneTransferError{Message: "IXFR response contains no SOA records"}
	}

	firstSOA, ok := soas[0].rr.(*dns.SOA)
	if !ok {
		return ZoneHistory{}, &ZoneTransferError{Message: "IXFR first SOA has unexpected type"}
	}
	currentSerial := firstSOA.Serial

	// AXFR-shaped fallback.
	if len(soas) == 2 {
		second, ok := soas[1].rr.(*dns.SOA)
		if ok && second.Serial == currentSerial {
			batch := HistoryBatch{FromSerial: 0, ToSerial: currentSerial}
			for _, rr := range all {
				if rr.Header().Rrtype == dns.TypeSOA {
					continue
				}
				batch.Changes = append(batch.Changes, historyChangeFromRR("add", rr))
			}
			batches := []HistoryBatch{}
			if len(batch.Changes) > 0 {
				batches = append(batches, batch)
			}
			return ZoneHistory{
				Zone:                zone,
				CurrentSerial:       currentSerial,
				Batches:             batches,
				IsFullAXFR:          true,
				AvailableFromSerial: 0,
			}, nil
		}
	}

	batches := make([]HistoryBatch, 0)
	availableFrom := currentSerial
	i := 1
	for i < len(soas)-1 {
		oldSOA, ok1 := soas[i].rr.(*dns.SOA)
		if !ok1 || i+1 >= len(soas) {
			break
		}
		newSOA, ok2 := soas[i+1].rr.(*dns.SOA)
		if !ok2 {
			return ZoneHistory{}, &ZoneTransferError{Message: "IXFR new SOA has unexpected type"}
		}
		oldSerial := oldSOA.Serial
		newSerial := newSOA.Serial
		if Less(oldSerial, availableFrom) {
			availableFrom = oldSerial
		}

		batch := HistoryBatch{FromSerial: oldSerial, ToSerial: newSerial}
		oldIdx := soas[i].idx
		newIdx := soas[i+1].idx
		for j := oldIdx + 1; j < newIdx; j++ {
			rr := all[j]
			if rr.Header().Rrtype == dns.TypeSOA {
				continue
			}
			batch.Changes = append(batch.Changes, historyChangeFromRR("delete", rr))
		}
		nextIdx := len(all)
		if i+2 < len(soas) {
			nextIdx = soas[i+2].idx
		}
		for j := newIdx + 1; j < nextIdx; j++ {
			rr := all[j]
			if rr.Header().Rrtype == dns.TypeSOA {
				continue
			}
			batch.Changes = append(batch.Changes, historyChangeFromRR("add", rr))
		}
		if len(batch.Changes) > 0 {
			batches = append(batches, batch)
		}
		i += 2
	}

	return ZoneHistory{
		Zone:                zone,
		CurrentSerial:       currentSerial,
		Batches:             batches,
		IsFullAXFR:          false,
		AvailableFromSerial: availableFrom,
	}, nil
}

func historyChangeFromRR(action string, rr dns.RR) HistoryChange {
	h := rr.Header()
	ttl := h.Ttl
	if action == "delete" {
		ttl = 0
	}
	return HistoryChange{
		Action:  action,
		Name:    dns.Fqdn(h.Name),
		TTL:     ttl,
		Type:    TypeName(h.Rrtype),
		RDClass: ClassName(h.Class),
		Records: []string{RdataText(rr)},
	}
}

// ReverseHistoryChanges reverses IXFR batches for rollback (newest→oldest, swap add/delete).
// Skips SOA and apex NS. Returns reversed changes and optional warning.
func ReverseHistoryChanges(batches []HistoryBatch, zone string) ([]HistoryChange, string) {
	zone = NormalizeZoneName(zone)
	out := make([]HistoryChange, 0)
	skipped := 0
	for bi := len(batches) - 1; bi >= 0; bi-- {
		batch := batches[bi]
		for ci := len(batch.Changes) - 1; ci >= 0; ci-- {
			ch := batch.Changes[ci]
			if isProtectedHistoryChange(ch, zone) {
				skipped++
				continue
			}
			rev := ch
			if ch.Action == "add" {
				rev.Action = "delete"
			} else {
				rev.Action = "add"
			}
			out = append(out, rev)
		}
	}
	var warning string
	if skipped > 0 {
		warning = fmt.Sprintf("Skipped %d protected record(s) (SOA/apex NS) during rollback", skipped)
	}
	return out, warning
}

func isProtectedHistoryChange(ch HistoryChange, zone string) bool {
	if ch.Type == "SOA" {
		return true
	}
	if ch.Type == "NS" {
		name := NormalizeZoneName(ch.Name)
		return name == zone
	}
	return false
}

// CheckZoneExists reports whether the server answers SOA for zone.
func (c *Client) CheckZoneExists(ctx context.Context, zone string) bool {
	_, err := c.QuerySOA(ctx, zone)
	return err == nil
}

// GetZoneHistory performs IXFR and returns history-shaped batches.
func (c *Client) GetZoneHistory(ctx context.Context, zone string, fromSerial uint32) (ZoneHistory, error) {
	zone = NormalizeZoneName(zone)

	logging.LogInternalEvent(c.log, "ixfr_history_start", slog.LevelInfo,
		slog.String("zone", zone),
		slog.Uint64("from_serial", uint64(fromSerial)),
	)

	var soa *dns.SOA
	if looked, err := c.querySOA(ctx, zone); err == nil {
		soa = looked
	}
	if fromSerial != 0 && soa != nil {
		if fromSerial == soa.Serial {
			return ZoneHistory{
				Zone:                zone,
				CurrentSerial:       soa.Serial,
				AvailableFromSerial: fromSerial,
			}, nil
		}
		if !Less(fromSerial, soa.Serial) {
			return ZoneHistory{}, &ZoneTransferError{
				Message: fmt.Sprintf(
					"IXFR not eligible: from_serial=%d not less than current=%d",
					fromSerial, soa.Serial,
				),
			}
		}
	}

	m := ixfrMsg(zone, fromSerial, soa)
	c.attachTSIG(m, c.axfrKeyName, c.axfrAlg)

	trs := &dns.Transfer{
		DialTimeout: c.timeout,
		ReadTimeout: c.axfrTO,
		TsigSecret:  c.tsigSecret,
	}
	if deadline, ok := ctx.Deadline(); ok {
		trs.ReadTimeout = time.Until(deadline)
	}

	env, err := trs.In(m, c.tcpAddr)
	if err != nil {
		metrics.IncZoneTransfersFailed("ixfr", zone)
		return ZoneHistory{}, &ZoneTransferError{
			Message: fmt.Sprintf("IXFR transfer failed for %s", zone),
			Cause:   err,
		}
	}
	var msgs []*dns.Msg
	for e := range env {
		if e.Error != nil {
			metrics.IncZoneTransfersFailed("ixfr", zone)
			return ZoneHistory{}, &ZoneTransferError{
				Message: fmt.Sprintf("IXFR transfer failed for %s", zone),
				Cause:   e.Error,
			}
		}
		msg := new(dns.Msg)
		msg.Answer = e.RR
		msgs = append(msgs, msg)
	}

	hist, err := ParseIXFRHistory(msgs, zone)
	if err != nil {
		metrics.IncZoneTransfersFailed("ixfr", zone)
		return ZoneHistory{}, err
	}
	if !hist.IsFullAXFR {
		metrics.IncZoneTransfers("ixfr", zone)
	}
	return hist, nil
}
