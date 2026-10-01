package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/scheduler"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

type schedOpBody struct {
	Action  string   `json:"action"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	RDClass string   `json:"rdclass,omitempty"`
	TTL     int      `json:"ttl,omitempty"`
	Records []string `json:"records,omitempty"`
}

type schedPrereqBody struct {
	PrereqType string  `json:"prereq_type"`
	Name       string  `json:"name"`
	RDType     *string `json:"rdtype,omitempty"`
	RDClass    string  `json:"rdclass,omitempty"`
	Data       *string `json:"data,omitempty"`
}

func registerScheduled(api huma.API, d *Deps) {
	requireStore := func() error {
		if d.Store == nil {
			return unavailable("scheduler store not available")
		}
		return nil
	}

	type createIn struct {
		Body struct {
			Name              string            `json:"name"`
			Description       *string           `json:"description,omitempty"`
			Zone              string            `json:"zone"`
			Operations        []schedOpBody     `json:"operations" minItems:"1"`
			Prerequisites     []schedPrereqBody `json:"prerequisites,omitempty"`
			ScheduledAt       *time.Time        `json:"scheduled_at,omitempty"`
			NotValidAfter     *time.Time        `json:"not_valid_after,omitempty"`
			AutoPrerequisites *bool             `json:"auto_prerequisites,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-scheduled-change", Method: http.MethodPost,
		Path: "/v1/scheduled-changes", Summary: "Create scheduled change",
		Tags: []string{"Scheduled Changes"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		zone := normalizeZone(in.Body.Zone)
		if d.Cache != nil {
			if cz := d.Cache.PeekZone(zone); cz == nil {
				if _, err := d.Cache.LoadZone(ctx, zone); err != nil {
					return nil, notFound("Zone '" + zone + "' not found")
				}
			}
		}
		auto := true
		if in.Body.AutoPrerequisites != nil {
			auto = *in.Body.AutoPrerequisites
		}
		u, _ := userFrom(ctx)
		ch, err := d.Store.Create(ctx, store.ChangeCreateData{
			Name: in.Body.Name, Description: in.Body.Description, Zone: zone,
			Operations: toAtomicOps(in.Body.Operations), Prerequisites: toPrereqs(in.Body.Prerequisites),
			ScheduledAt: in.Body.ScheduledAt, NotValidAfter: in.Body.NotValidAfter,
			AutoPrerequisites: auto, CreatedBy: actorPtr(u.ID),
		})
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return &struct{ Body map[string]any }{Body: scheduledChangeResponse(ch)}, nil
	})

	type listIn struct {
		Status []string `query:"status"`
		Zone   string   `query:"zone"`
		Source string   `query:"source"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-scheduled-changes", Method: http.MethodGet,
		Path: "/v1/scheduled-changes", Summary: "List scheduled changes",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *listIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		opts := store.ListChangesOpts{Statuses: in.Status, Zone: in.Zone, Source: in.Source}
		if len(in.Status) == 1 {
			opts.Status = in.Status[0]
			opts.Statuses = nil
		}
		changes, err := d.Store.ListChanges(ctx, opts)
		if err != nil {
			return nil, internalErr(err.Error())
		}
		out := make([]map[string]any, 0, len(changes))
		for _, c := range changes {
			out = append(out, scheduledChangeResponse(c))
		}
		return &struct{ Body map[string]any }{Body: map[string]any{"changes": out, "total": len(out)}}, nil
	})

	type eventsListIn struct {
		Event    string `query:"event"`
		Actor    string `query:"actor"`
		Zone     string `query:"zone"`
		ChangeID string `query:"change_id"`
		Q        string `query:"q"`
		Since    string `query:"since"`
		Until    string `query:"until"`
		Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
		Offset   int    `query:"offset" minimum:"0"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-events", Method: http.MethodGet,
		Path: "/v1/scheduled-changes/events", Summary: "List audit events",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *eventsListIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		var eventsFilter []string
		if in.Event != "" {
			eventsFilter = []string{in.Event}
		}
		var since, until *time.Time
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, badRequest("invalid since timestamp")
			}
			since = &t
		}
		if in.Until != "" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, badRequest("invalid until timestamp")
			}
			until = &t
		}
		events, total, err := d.Store.ListEvents(ctx, store.ListEventsOpts{
			Events: eventsFilter, Actor: in.Actor, Zone: in.Zone, ChangeID: in.ChangeID,
			Q: in.Q, Since: since, Until: until, Limit: in.Limit, Offset: in.Offset,
		})
		if err != nil {
			return nil, internalErr(err.Error())
		}
		out := make([]map[string]any, 0, len(events))
		for _, e := range events {
			item := map[string]any{
				"id": e.ID, "ts": e.TS.UTC().Format(time.RFC3339Nano), "event": e.Event,
				"change_id": e.ChangeID, "change_name": e.ChangeName,
				"zone": e.Zone, "change_status": e.ChangeStatus,
			}
			if e.Actor != nil {
				item["actor"] = *e.Actor
			}
			if e.Detail != nil {
				item["detail"] = e.Detail
			}
			out = append(out, item)
		}
		return &struct{ Body map[string]any }{Body: map[string]any{"events": out, "total": total}}, nil
	})

	type idIn struct {
		ChangeID string `path:"change_id"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-scheduled-change", Method: http.MethodGet,
		Path: "/v1/scheduled-changes/{change_id}", Summary: "Get scheduled change",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		ch, err := d.Store.Get(ctx, in.ChangeID, true)
		if err != nil || ch == nil {
			return nil, notFound("change not found")
		}
		return &struct{ Body map[string]any }{Body: scheduledChangeResponse(ch)}, nil
	})

	type patchIn struct {
		ChangeID string `path:"change_id"`
		Body     struct {
			Name              *string           `json:"name,omitempty"`
			Description       *string           `json:"description,omitempty"`
			Operations        []schedOpBody     `json:"operations,omitempty"`
			Prerequisites     []schedPrereqBody `json:"prerequisites,omitempty"`
			ScheduledAt       *time.Time        `json:"scheduled_at,omitempty"`
			NotValidAfter     *time.Time        `json:"not_valid_after,omitempty"`
			AutoPrerequisites *bool             `json:"auto_prerequisites,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "update-scheduled-change", Method: http.MethodPatch,
		Path: "/v1/scheduled-changes/{change_id}", Summary: "Update scheduled change",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *patchIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		u, _ := userFrom(ctx)
		data := store.ChangeUpdateData{
			Name: in.Body.Name, Description: in.Body.Description,
			AutoPrerequisites: in.Body.AutoPrerequisites,
			ScheduledAt:       in.Body.ScheduledAt, NotValidAfter: in.Body.NotValidAfter,
			Actor: actorPtr(u.ID),
		}
		if in.Body.Operations != nil {
			data.Operations = toAtomicOps(in.Body.Operations)
		}
		if in.Body.Prerequisites != nil {
			data.Prerequisites = toPrereqs(in.Body.Prerequisites)
			data.HasPrerequisites = true
		}
		ch, err := d.Store.Update(ctx, in.ChangeID, data)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, notFound("change not found")
			}
			if errors.Is(err, store.ErrInvalidStatus) {
				return nil, conflict(err.Error())
			}
			return nil, badRequest(err.Error())
		}
		return &struct{ Body map[string]any }{Body: scheduledChangeResponse(ch)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancel-scheduled-change", Method: http.MethodDelete,
		Path: "/v1/scheduled-changes/{change_id}", Summary: "Cancel scheduled change",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		u, _ := userFrom(ctx)
		ch, err := d.Store.Cancel(ctx, in.ChangeID, actorPtr(u.ID))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, notFound("change not found")
			}
			if errors.Is(err, store.ErrInvalidStatus) {
				return nil, conflict(err.Error())
			}
			return nil, badRequest(err.Error())
		}
		return &struct{ Body map[string]any }{Body: scheduledChangeResponse(ch)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "apply-scheduled-change", Method: http.MethodPost,
		Path: "/v1/scheduled-changes/{change_id}/apply", Summary: "Apply scheduled change now",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		if d.Client == nil {
			return nil, unavailable("DNS client not initialized")
		}
		ch, err := d.Store.Get(ctx, in.ChangeID, false)
		if err != nil || ch == nil {
			return nil, notFound("change not found")
		}
		u, _ := userFrom(ctx)
		if _, err := d.Store.MarkRunning(ctx, ch.ID, "manual-apply", 2*time.Minute); err != nil {
			return nil, conflict(err.Error())
		}
		ch, _ = d.Store.Get(ctx, in.ChangeID, false)
		result := scheduler.ExecuteChange(ctx, ch, d.Store, d.Client, d.Cache, scheduler.ExecuteOpts{
			MaxAttempts: d.Settings.Scheduler.MaxAttempts, RetryBackoff: d.Settings.Scheduler.RetryBackoff,
			Actor: actorPtr(u.ID), Trigger: notifications.TriggerManual,
		})
		status := store.StatusApplied
		if !result.Success {
			status = store.StatusFailed
		}
		body := map[string]any{
			"success": result.Success, "change_id": in.ChangeID, "zone": ch.Zone,
			"status": status, "message": result.Message,
		}
		if result.ResultRcode != nil {
			body["result_rcode"] = *result.ResultRcode
		}
		if result.NewSerial != nil {
			body["new_serial"] = *result.NewSerial
		}
		return &struct{ Body map[string]any }{Body: body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-scheduled-change", Method: http.MethodPost,
		Path: "/v1/scheduled-changes/{change_id}/preview", Summary: "Preview scheduled change",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		ch, err := d.Store.Get(ctx, in.ChangeID, false)
		if err != nil || ch == nil {
			return nil, notFound("change not found")
		}
		zone := normalizeZone(ch.Zone)
		lookup, typesAt := cacheLookupFuncs(d, zone)
		ops := scheduler.ForwardOpsToAtomic(ch.Operations)
		prereqs := make([]dnsx.Prerequisite, 0, len(ch.Prerequisites))
		for _, p := range ch.Prerequisites {
			pr := dnsx.Prerequisite{Type: p.PrereqType, Name: p.Name, Class: p.RDClass}
			if p.RDType != nil {
				pr.RdType = *p.RDType
			}
			if p.Data != nil {
				pr.Data = *p.Data
			}
			prereqs = append(prereqs, pr)
		}
		built, err := dnsx.BuildUpdate(zone, ops, prereqs, ch.AutoPrerequisites, true, lookup, typesAt)
		prereqOut := []map[string]any{}
		allPassed := true
		if err != nil {
			allPassed = false
			prereqOut = append(prereqOut, map[string]any{
				"prereq_type": "build", "name": zone, "passed": false,
				"message": err.Error(), "source": "build",
			})
		} else {
			for _, p := range built.AutoPrerequisites {
				prereqOut = append(prereqOut, map[string]any{
					"prereq_type": p.PrereqType, "name": p.Name, "rdtype": p.RdType,
					"rdclass": p.Class, "data": p.Data, "passed": p.Passed,
					"message": p.Message, "source": p.Source,
				})
				if !p.Passed {
					allPassed = false
				}
			}
		}
		conflicts, _ := d.Store.FindConflicts(ctx, zone, scheduledOpsToAtomic(ch.Operations), ch.ID)
		confOut := make([]map[string]any, 0, len(conflicts))
		for _, c := range conflicts {
			confOut = append(confOut, map[string]any{
				"other_change_id": c.OtherID, "other_change_name": c.OtherName,
				"name": c.Name, "type": c.Type, "rdclass": c.RDClass,
			})
		}
		msg := "All prerequisites passed"
		if !allPassed {
			msg = "Some prerequisites would fail"
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"change_id": ch.ID, "zone": zone, "prerequisites": prereqOut,
			"all_prerequisites_passed": allPassed, "conflicts": confOut,
			"operations_count": len(ch.Operations), "message": msg,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-change-events", Method: http.MethodGet,
		Path: "/v1/scheduled-changes/{change_id}/events", Summary: "List events for a change",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body []map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		if ch, err := d.Store.Get(ctx, in.ChangeID, false); err != nil || ch == nil {
			return nil, notFound("change not found")
		}
		events, err := d.Store.GetEvents(ctx, in.ChangeID)
		if err != nil {
			return nil, internalErr(err.Error())
		}
		out := make([]map[string]any, 0, len(events))
		for _, e := range events {
			item := map[string]any{
				"id": e.ID, "change_id": e.ChangeID,
				"ts": e.TS.UTC().Format(time.RFC3339Nano), "event": e.Event,
			}
			if e.Actor != nil {
				item["actor"] = *e.Actor
			}
			if e.Detail != nil {
				item["detail"] = e.Detail
			}
			out = append(out, item)
		}
		return &struct{ Body []map[string]any }{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "revert-preview", Method: http.MethodGet,
		Path: "/v1/scheduled-changes/{change_id}/revert-preview", Summary: "Preview revert",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		ch, err := d.Store.Get(ctx, in.ChangeID, false)
		if err != nil || ch == nil {
			return nil, notFound("change not found")
		}
		if !scheduler.CanRevert(ch) {
			return nil, conflict("change cannot be reverted")
		}
		ops, err := scheduler.BuildRevertOps(ch)
		if err != nil {
			return nil, conflict(err.Error())
		}
		opOut := make([]map[string]any, 0, len(ops))
		for _, op := range ops {
			opOut = append(opOut, map[string]any{
				"action": op.Action, "name": op.Name, "type": op.Type,
				"rdclass": op.RDClass, "ttl": op.TTL, "records": op.Records,
			})
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"change_id": ch.ID, "zone": ch.Zone, "operations": opOut,
			"warning": scheduler.RevertWarning, "message": "Revert preview", "can_revert": true,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "revert-scheduled-change", Method: http.MethodPost,
		Path: "/v1/scheduled-changes/{change_id}/revert", Summary: "Revert applied change",
		Tags: []string{"Scheduled Changes"},
	}, func(ctx context.Context, in *idIn) (*struct{ Body map[string]any }, error) {
		if err := requireStore(); err != nil {
			return nil, err
		}
		if d.Client == nil {
			return nil, unavailable("DNS client not initialized")
		}
		ch, err := d.Store.Get(ctx, in.ChangeID, false)
		if err != nil || ch == nil {
			return nil, notFound("change not found")
		}
		if !scheduler.CanRevert(ch) {
			return nil, conflict("change cannot be reverted")
		}
		ops, err := scheduler.BuildRevertOps(ch)
		if err != nil {
			return nil, conflict(err.Error())
		}
		zone := normalizeZone(ch.Zone)
		dnsOps := scheduler.RevertOpsToAtomic(ops)
		lookup, typesAt := cacheLookupFuncs(d, zone)
		built, err := dnsx.BuildUpdate(zone, dnsOps, nil, false, false, lookup, typesAt)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		u, _ := userFrom(ctx)
		cc := notifications.ChangeContext{
			Trigger: notifications.TriggerRevert, ChangeID: &ch.ID, ChangeName: &ch.Name,
			Actor: actorPtr(u.ID),
		}
		if _, err := d.Client.SendUpdate(notifications.WithChangeContext(ctx, cc), zone, built.Msg); err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		if d.Cache != nil {
			if cz := d.Cache.PeekZone(zone); cz != nil && cz.Zone != nil {
				_ = dnsx.ApplyCacheUpdates(cz.Zone, built.CacheUpdates)
			}
		}
		var newSerial *int64
		if d.Cache != nil {
			if cz := d.Cache.PeekZone(zone); cz != nil {
				s := int64(cz.Serial)
				newSerial = &s
			}
		}
		rcode := "NOERROR"
		ch, err = d.Store.MarkReverted(ctx, ch.ID, store.MarkRevertedOpts{
			Actor: actorPtr(u.ID), ResultRcode: &rcode, NewSerial: newSerial, OperationsCount: len(ops),
		})
		if err != nil {
			return nil, internalErr(err.Error())
		}
		opOut := make([]map[string]any, 0, len(ops))
		for _, op := range ops {
			opOut = append(opOut, map[string]any{
				"action": op.Action, "name": op.Name, "type": op.Type,
				"rdclass": op.RDClass, "ttl": op.TTL, "records": op.Records,
			})
		}
		body := map[string]any{
			"success": true, "change_id": ch.ID, "zone": zone, "status": ch.Status,
			"message": "Change reverted", "result_rcode": rcode, "operations": opOut,
		}
		if newSerial != nil {
			body["new_serial"] = *newSerial
		}
		return &struct{ Body map[string]any }{Body: body}, nil
	})
}

func toAtomicOps(ops []schedOpBody) []store.AtomicOperation {
	out := make([]store.AtomicOperation, 0, len(ops))
	for _, o := range ops {
		rd := o.RDClass
		if rd == "" {
			rd = "IN"
		}
		out = append(out, store.AtomicOperation{
			Action: strings.ToLower(o.Action), Name: o.Name, Type: strings.ToUpper(o.Type),
			RDClass: rd, TTL: o.TTL, Records: o.Records,
		})
	}
	return out
}

func toPrereqs(ps []schedPrereqBody) []store.ChangePrerequisite {
	out := make([]store.ChangePrerequisite, 0, len(ps))
	for _, p := range ps {
		rd := p.RDClass
		if rd == "" {
			rd = "IN"
		}
		out = append(out, store.ChangePrerequisite{
			PrereqType: p.PrereqType, Name: p.Name, RDType: p.RDType, RDClass: rd, Data: p.Data,
		})
	}
	return out
}

func scheduledOpsToAtomic(ops []store.ScheduledOperation) []store.AtomicOperation {
	out := make([]store.AtomicOperation, 0, len(ops))
	for _, o := range ops {
		out = append(out, store.AtomicOperation{
			Action: o.Action, Name: o.Name, Type: o.Type, RDClass: o.RDClass, TTL: o.TTL, Records: o.Records,
		})
	}
	return out
}

func cacheLookupFuncs(d *Deps, zone string) (dnsx.LookupFunc, dnsx.TypesAtNameFunc) {
	lookup := func(name, typ string) (uint32, []string, bool) {
		if d.Cache == nil {
			return 0, nil, false
		}
		cz := d.Cache.PeekZone(zone)
		if cz == nil || cz.Zone == nil {
			return 0, nil, false
		}
		tc, err := dnsx.TypeFromString(typ)
		if err != nil {
			return 0, nil, false
		}
		info, ok := cz.Zone.GetRRset(name, tc)
		if !ok {
			return 0, nil, false
		}
		return info.TTL, info.Records, true
	}
	typesAt := func(name string) []string {
		if d.Cache == nil {
			return nil
		}
		cz := d.Cache.PeekZone(zone)
		if cz == nil || cz.Zone == nil {
			return nil
		}
		return cz.Zone.TypesAtName(name)
	}
	return lookup, typesAt
}
