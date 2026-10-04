package httpapi

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func normalizeZone(zone string) string {
	return dnsx.NormalizeZoneName(zone)
}

// PaginatedZones is the list-zones response.
type PaginatedZones struct {
	Zones      []ZoneSummary `json:"zones"`
	TotalCount int           `json:"total_count"`
	HasMore    bool          `json:"has_more"`
	NextCursor *string       `json:"next_cursor"`
	PageSize   *int          `json:"page_size,omitempty"`
}

type ZoneSummary struct {
	Zone        string  `json:"zone"`
	Serial      uint32  `json:"serial"`
	RRsetCount  int     `json:"rrset_count"`
	LastRefresh string  `json:"last_refresh"`
	ZoneIsIDN   bool    `json:"zone_is_idn"`
	ZoneUTF8    *string `json:"zone_utf8,omitempty"`
}

type ZoneDetail struct {
	Zone        string  `json:"zone"`
	Serial      uint32  `json:"serial"`
	RRsetCount  int     `json:"rrset_count"`
	LastRefresh string  `json:"last_refresh"`
	SOARefresh  uint32  `json:"soa_refresh,omitempty"`
	ZoneIsIDN   bool    `json:"zone_is_idn"`
	ZoneUTF8    *string `json:"zone_utf8,omitempty"`
}

func zoneSummaryFrom(cz *dnsx.CachedZone) ZoneSummary {
	info := dnsx.GetIDNInfo(cz.ZoneName)
	s := ZoneSummary{
		Zone:        cz.ZoneName,
		Serial:      cz.Serial,
		LastRefresh: cz.LastRefresh.UTC().Format(time.RFC3339Nano),
		ZoneIsIDN:   info["has_idn"].(bool),
	}
	if cz.Zone != nil {
		s.RRsetCount = cz.Zone.RRsetCount()
	}
	if u, ok := info["unicode"].(string); ok && u != "" {
		s.ZoneUTF8 = &u
	}
	return s
}

func zoneDetailFrom(cz *dnsx.CachedZone) ZoneDetail {
	sum := zoneSummaryFrom(cz)
	d := ZoneDetail{
		Zone:        sum.Zone,
		Serial:      sum.Serial,
		RRsetCount:  sum.RRsetCount,
		LastRefresh: sum.LastRefresh,
		SOARefresh:  cz.SOARefresh,
		ZoneIsIDN:   sum.ZoneIsIDN,
		ZoneUTF8:    sum.ZoneUTF8,
	}
	return d
}

type RRsetResponse struct {
	Name          string   `json:"name"`
	TTL           uint32   `json:"ttl"`
	Type          string   `json:"type"`
	RDClass       string   `json:"rdclass"`
	Records       []string `json:"records"`
	NameIsIDN     bool     `json:"name_is_idn"`
	NameUTF8      *string  `json:"name_utf8,omitempty"`
	RecordsIsUTF8 bool     `json:"records_is_utf8"`
	RecordsUTF8   []string `json:"records_utf8,omitempty"`
}

func rrsetFromInfo(info dnsx.RRsetInfo) RRsetResponse {
	idn := dnsx.GetIDNInfo(info.Name)
	utf8 := dnsx.GetRecordsUTF8Info(info.Records)
	out := RRsetResponse{
		Name:          info.Name,
		TTL:           info.TTL,
		Type:          info.Type,
		RDClass:       info.Class,
		Records:       info.Records,
		NameIsIDN:     idn["has_idn"].(bool),
		RecordsIsUTF8: utf8.HasUTF8,
	}
	if u, ok := idn["unicode"].(string); ok && u != "" {
		out.NameUTF8 = &u
	}
	if utf8.HasUTF8 {
		out.RecordsUTF8 = utf8.Records
	}
	return out
}

type PaginatedRRsets struct {
	RRsets     []RRsetResponse `json:"rrsets"`
	TotalCount int             `json:"total_count"`
	HasMore    bool            `json:"has_more"`
	NextCursor *string         `json:"next_cursor"`
	PageSize   *int            `json:"page_size,omitempty"`
}

type PaginatedResults struct {
	Results    []RRsetResponse `json:"results"`
	TotalCount int             `json:"total_count"`
	HasMore    bool            `json:"has_more"`
	NextCursor *string         `json:"next_cursor"`
	PageSize   *int            `json:"page_size,omitempty"`
	Zone       string          `json:"zone,omitempty"`
	Serial     uint32          `json:"serial,omitempty"`
}

func scheduledChangeResponse(c *store.ScheduledChange) map[string]any {
	ops := make([]map[string]any, 0, len(c.Operations))
	for _, op := range c.Operations {
		m := map[string]any{
			"action":  op.Action,
			"name":    op.Name,
			"type":    op.Type,
			"rdclass": op.RDClass,
			"ttl":     op.TTL,
			"records": op.Records,
		}
		if op.PriorTTL != nil {
			m["prior_ttl"] = *op.PriorTTL
		}
		if op.PriorRecords != nil {
			m["prior_records"] = op.PriorRecords
		}
		if op.SnapshotAt != nil {
			m["snapshot_at"] = op.SnapshotAt.UTC().Format(time.RFC3339Nano)
		}
		ops = append(ops, m)
	}
	prereqs := make([]map[string]any, 0, len(c.Prerequisites))
	for _, p := range c.Prerequisites {
		m := map[string]any{
			"prereq_type": p.PrereqType,
			"name":        p.Name,
			"rdclass":     p.RDClass,
		}
		if p.RDType != nil {
			m["rdtype"] = *p.RDType
		}
		if p.Data != nil {
			m["data"] = *p.Data
		}
		prereqs = append(prereqs, m)
	}
	events := make([]map[string]any, 0, len(c.Events))
	for _, e := range c.Events {
		em := map[string]any{
			"id":        e.ID,
			"change_id": e.ChangeID,
			"ts":        e.TS.UTC().Format(time.RFC3339Nano),
			"event":     e.Event,
		}
		if e.Actor != nil {
			em["actor"] = *e.Actor
		}
		if e.Detail != nil {
			em["detail"] = e.Detail
		}
		events = append(events, em)
	}
	out := map[string]any{
		"id":                 c.ID,
		"name":               c.Name,
		"zone":               c.Zone,
		"status":             c.Status,
		"source":             c.Source,
		"auto_prerequisites": c.AutoPrerequisites,
		"created_at":         c.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":         c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"attempts":           c.Attempts,
		"operations":         ops,
		"prerequisites":      prereqs,
		"events":             events,
		"kind":               c.Kind,
	}
	if out["kind"] == "" {
		out["kind"] = store.KindRecords
	}
	if len(c.Payload) > 0 {
		var payload any
		if err := json.Unmarshal(c.Payload, &payload); err == nil {
			out["payload"] = payload
		}
	}
	if c.Description != nil {
		out["description"] = *c.Description
	}
	if c.ScheduledAt != nil {
		out["scheduled_at"] = c.ScheduledAt.UTC().Format(time.RFC3339Nano)
	}
	if c.NotValidAfter != nil {
		out["not_valid_after"] = c.NotValidAfter.UTC().Format(time.RFC3339Nano)
	}
	if c.CreatedBy != nil {
		out["created_by"] = *c.CreatedBy
	}
	if c.NextAttemptAt != nil {
		out["next_attempt_at"] = c.NextAttemptAt.UTC().Format(time.RFC3339Nano)
	}
	if c.LastError != nil {
		out["last_error"] = *c.LastError
	}
	if c.AppliedAt != nil {
		out["applied_at"] = c.AppliedAt.UTC().Format(time.RFC3339Nano)
	}
	if c.ResultRcode != nil {
		out["result_rcode"] = *c.ResultRcode
	}
	if c.NewSerial != nil {
		out["new_serial"] = *c.NewSerial
	}
	if c.RevertedAt != nil {
		out["reverted_at"] = c.RevertedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func actorPtr(id string) *string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return &id
}
