package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/provision"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

// ZoneProvisioner applies zone create/delete/catalog scheduled changes.
type ZoneProvisioner interface {
	CreateZone(ctx context.Context, req provision.CreateRequest) (provision.Result, error)
	DeleteZone(ctx context.Context, zone string, opts provision.DeleteOptions) (provision.Result, error)
	AddToCatalog(ctx context.Context, zone string) (provision.Result, error)
	RemoveFromCatalog(ctx context.Context, zone string) (provision.Result, error)
}

// ExecutionResult is the outcome of executing a scheduled change.
type ExecutionResult struct {
	Success     bool
	Message     string
	ResultRcode *string
	NewSerial   *int64
	Error       string
}

// ExecuteOpts configures ExecuteChange.
type ExecuteOpts struct {
	MaxAttempts  int
	RetryBackoff time.Duration
	Actor        *string
	Trigger      string
	Log          *slog.Logger
	Provisioner  ZoneProvisioner
}

// ExecuteChange builds and sends the DDNS UPDATE for a claimed scheduled change.
func ExecuteChange(
	ctx context.Context,
	change *store.ScheduledChange,
	st *store.Store,
	client *dnsx.Client,
	cache *dnsx.ZoneCache,
	opts ExecuteOpts,
) ExecutionResult {
	if change == nil {
		return ExecutionResult{Success: false, Message: "nil change", Error: "nil change"}
	}
	trigger := opts.Trigger
	if trigger == "" {
		trigger = notifications.TriggerScheduler
	}
	zone := change.Zone
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}

	kind := change.Kind
	if kind == "" {
		kind = store.KindRecords
	}
	switch kind {
	case store.KindZoneCreate, store.KindZoneDelete, store.KindZoneCatalogAdd, store.KindZoneCatalogRemove:
		return executeZoneChange(ctx, change, st, opts, kind, zone)
	}

	if cache != nil {
		if _, err := cache.RefreshZone(ctx, zone, false); err != nil {
			logging.LogInternalEvent(opts.Log, "cache_refresh_before_scheduled_change_failed", slog.LevelWarn,
				slog.String("zone", zone),
				slog.String("error", err.Error()),
			)
		}
	}

	snapshots := CapturePriorState(zone, change.Operations, cache, time.Now().UTC())
	if err := st.SaveOperationSnapshots(ctx, change.ID, snapshots); err != nil {
		return failChange(ctx, st, change.ID, fmt.Sprintf("save snapshots: %v", err), opts, "unexpected", nil)
	}

	lookup := func(name, typ string) (uint32, []string, bool) {
		info := lookupRRset(cache, zone, name, typ)
		if info == nil {
			return 0, nil, false
		}
		return info.TTL, info.Records, true
	}
	typesAtName := func(name string) []string {
		if cache == nil {
			return nil
		}
		cz := cache.PeekZone(zone)
		if cz == nil || cz.Zone == nil {
			return nil
		}
		return cz.Zone.TypesAtName(name)
	}

	prereqs := make([]dnsx.Prerequisite, 0, len(change.Prerequisites))
	for _, p := range change.Prerequisites {
		pr := dnsx.Prerequisite{
			Type:  p.PrereqType,
			Name:  p.Name,
			Class: p.RDClass,
		}
		if p.RDType != nil {
			pr.RdType = *p.RDType
		}
		if p.Data != nil {
			pr.Data = *p.Data
		}
		prereqs = append(prereqs, pr)
	}

	built, err := dnsx.BuildUpdate(
		zone,
		ForwardOpsToAtomic(change.Operations),
		prereqs,
		change.AutoPrerequisites,
		false,
		lookup,
		typesAtName,
	)
	if err != nil {
		msg := err.Error()
		var ube *dnsx.UpdateBuildError
		if errors.As(err, &ube) {
			msg = ube.Message
		}
		metrics.IncDDNSUpdatesFailed("build_error")
		return failChange(ctx, st, change.ID, msg, opts, "build_error", nil)
	}

	cc := notifications.ChangeContext{
		Trigger:    trigger,
		ChangeID:   &change.ID,
		ChangeName: &change.Name,
		Actor:      opts.Actor,
	}
	sendCtx := notifications.WithChangeContext(ctx, cc)

	_, sendErr := client.SendUpdate(sendCtx, zone, built.Msg)
	if sendErr != nil {
		var pre *dnsx.PrerequisiteError
		if errors.As(sendErr, &pre) {
			if cache != nil {
				_, _ = cache.RefreshZone(ctx, zone, false)
			}
			rcode := pre.RcodeText
			return failChange(ctx, st, change.ID, pre.Error(), opts, "prereq_failed", &rcode)
		}
		return failChange(ctx, st, change.ID, sendErr.Error(), opts, "update_error", nil)
	}

	if cache != nil {
		for _, cu := range built.CacheUpdates {
			switch strings.ToLower(cu.Action) {
			case "add":
				cache.UpdateCacheAfterAdd(zone, cu.Name, cu.TTL, cu.RdType, cu.Class, cu.Records)
				metrics.IncRRsetAdds(zone)
			case "delete":
				cache.UpdateCacheAfterDelete(zone, cu.Name, cu.RdType, cu.Class, cu.Records)
				metrics.IncRRsetDeletes(zone)
			case "replace":
				cache.UpdateCacheAfterReplace(zone, cu.Name, cu.TTL, cu.RdType, cu.Class, cu.Records)
				metrics.IncRRsetReplaces(zone)
			}
		}
	}

	var newSerial *int64
	if cache != nil {
		if cz := cache.PeekZone(zone); cz != nil {
			s := int64(cz.Serial)
			newSerial = &s
		}
	}

	if change.ScheduledAt != nil && change.AppliedAt == nil {
		lateness := time.Since(change.ScheduledAt.UTC()).Seconds()
		if lateness < 0 {
			lateness = 0
		}
		metrics.ObserveScheduledChangeLateness(lateness)
	}

	rcode := "NOERROR"
	if _, err := st.MarkApplied(ctx, change.ID, store.MarkAppliedOpts{
		ResultRcode: &rcode,
		NewSerial:   newSerial,
		Actor:       opts.Actor,
		Trigger:     trigger,
	}); err != nil {
		return ExecutionResult{Success: false, Message: err.Error(), Error: err.Error()}
	}
	metrics.IncScheduledChangesApplied(trigger)

	return ExecutionResult{
		Success:     true,
		Message:     "Change applied successfully",
		ResultRcode: &rcode,
		NewSerial:   newSerial,
	}
}

func executeZoneChange(
	ctx context.Context,
	change *store.ScheduledChange,
	st *store.Store,
	opts ExecuteOpts,
	kind, zone string,
) ExecutionResult {
	if opts.Provisioner == nil {
		return failChange(ctx, st, change.ID, "zone provisioning is not configured", opts, "rndc_not_configured", nil)
	}
	var (
		res provision.Result
		err error
	)
	switch kind {
	case store.KindZoneCreate:
		var req provision.CreateRequest
		if len(change.Payload) > 0 {
			if uerr := json.Unmarshal(change.Payload, &req); uerr != nil {
				return failChange(ctx, st, change.ID, "invalid zone_create payload: "+uerr.Error(), opts, "build_error", nil)
			}
		}
		req.Zone = zone
		res, err = opts.Provisioner.CreateZone(ctx, req)
	case store.KindZoneDelete:
		var delOpts provision.DeleteOptions
		if len(change.Payload) > 0 {
			if uerr := json.Unmarshal(change.Payload, &delOpts); uerr != nil {
				return failChange(ctx, st, change.ID, "invalid zone_delete payload: "+uerr.Error(), opts, "build_error", nil)
			}
		}
		res, err = opts.Provisioner.DeleteZone(ctx, zone, delOpts)
	case store.KindZoneCatalogAdd:
		res, err = opts.Provisioner.AddToCatalog(ctx, zone)
	case store.KindZoneCatalogRemove:
		res, err = opts.Provisioner.RemoveFromCatalog(ctx, zone)
	default:
		return failChange(ctx, st, change.ID, "unsupported change kind "+kind, opts, "build_error", nil)
	}
	if err != nil {
		return failChange(ctx, st, change.ID, err.Error(), opts, "update_error", nil)
	}
	var newSerial *int64
	if res.Serial != nil {
		s := int64(*res.Serial)
		newSerial = &s
	}
	rcode := "NOERROR"
	if _, err := st.MarkApplied(ctx, change.ID, store.MarkAppliedOpts{
		ResultRcode: &rcode,
		NewSerial:   newSerial,
		Actor:       opts.Actor,
		Trigger:     opts.Trigger,
	}); err != nil {
		return ExecutionResult{Success: false, Message: err.Error(), Error: err.Error()}
	}
	metrics.IncScheduledChangesApplied(opts.Trigger)
	return ExecutionResult{
		Success:     true,
		Message:     "Change applied successfully",
		ResultRcode: &rcode,
		NewSerial:   newSerial,
	}
}

func failChange(
	ctx context.Context,
	st *store.Store,
	changeID, errMsg string,
	opts ExecuteOpts,
	reason string,
	rcode *string,
) ExecutionResult {
	metrics.IncScheduledChangesFailed(reason)
	_, _ = st.MarkFailed(ctx, changeID, errMsg, store.MarkFailedOpts{
		MaxAttempts:  opts.MaxAttempts,
		RetryBackoff: opts.RetryBackoff,
		Actor:        opts.Actor,
		ResultRcode:  rcode,
	})
	return ExecutionResult{
		Success:     false,
		Message:     errMsg,
		ResultRcode: rcode,
		Error:       errMsg,
	}
}
