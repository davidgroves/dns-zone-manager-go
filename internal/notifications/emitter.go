package notifications

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

// EventEmitter records applied DNS changes and enqueues webhook outbox rows.
// It implements dnsx.ChangeHooks when wired as Client.Hooks.
type EventEmitter struct {
	Store    *store.Store
	Settings config.WebhookSettings
	Server   string
	Log      *slog.Logger
}

// OnUpdateResult implements dnsx.ChangeHooks.
func (e *EventEmitter) OnUpdateResult(ctx context.Context, zone string, msg *dns.Msg, resp *dns.Msg, err error) {
	if e == nil || !e.Settings.Enabled {
		return
	}
	if err != nil {
		rcode := ""
		errMsg := err.Error()
		if resp != nil {
			rcode = dns.RcodeToString[resp.Rcode]
		}
		_ = e.EmitFailed(ctx, zone, msg, rcode, errMsg)
		return
	}
	rcode := "NOERROR"
	if resp != nil {
		rcode = dns.RcodeToString[resp.Rcode]
	}
	_ = e.EmitApplied(ctx, zone, msg, rcode)
}

// EmitApplied builds a change_applied event, optionally autorecords it, and
// enqueues outbox rows for matching targets — all in one store transaction.
func (e *EventEmitter) EmitApplied(ctx context.Context, zone string, msg *dns.Msg, rcode string) error {
	return e.emit(ctx, zone, msg, EventChangeApplied, rcode, "")
}

// EmitFailed builds a change_failed event and enqueues matching targets.
func (e *EventEmitter) EmitFailed(ctx context.Context, zone string, msg *dns.Msg, rcode, errMsg string) error {
	return e.emit(ctx, zone, msg, EventChangeFailed, rcode, errMsg)
}

func (e *EventEmitter) emit(ctx context.Context, zone string, msg *dns.Msg, eventType, rcode, errMsg string) error {
	if e == nil || e.Store == nil || !e.Settings.Enabled {
		return nil
	}
	if !stringsHasSuffixDot(zone) {
		zone += "."
	}

	cc := FromContext(ctx)
	ops := OperationsFromUpdate(msg)

	mintedID := false
	var changeID *string
	if cc.ChangeID != nil && *cc.ChangeID != "" {
		changeID = cc.ChangeID
	} else {
		id := uuid.NewString()
		changeID = &id
		mintedID = true
	}

	trigger := cc.Trigger
	if trigger == "" {
		trigger = TriggerManual
	}

	var rcodePtr *string
	if rcode != "" {
		rcodePtr = &rcode
	}
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	var serverPtr *string
	if e.Server != "" {
		serverPtr = &e.Server
	}

	ev := DnsChangeEvent{
		Event:      eventType,
		Zone:       zone,
		Operations: ops,
		Timestamp:  time.Now().UTC(),
		Trigger:    trigger,
		Actor:      cc.Actor,
		ActorName:  cc.ActorName,
		ActorEmail: cc.ActorEmail,
		AuthType:   cc.AuthType,
		ChangeID:   changeID,
		ChangeName: cc.ChangeName,
		RequestID:  cc.RequestID,
		Server:     serverPtr,
		Rcode:      rcodePtr,
		Error:      errPtr,
		Autorecord: mintedID,
	}

	targets := MatchingTargets(e.Settings, eventType, zone)

	err := e.Store.RunInTx(ctx, "emit_change_event", func(ctx context.Context) error {
		if ev.Autorecord {
			if !e.Settings.AutorecordManualChanges {
				metrics.IncWebhookAutorecordedChanges("skipped")
				ev.ChangeID = nil
				ev.Autorecord = false
			} else {
				status := store.StatusApplied
				if !ev.Succeeded() {
					status = store.StatusFailed
				}
				opsStore := make([]store.AtomicOperation, 0, len(ops))
				for _, op := range ops {
					ttl := 3600
					if op.TTL != nil {
						ttl = *op.TTL
					}
					opsStore = append(opsStore, store.AtomicOperation{
						Action:  op.Action,
						Name:    op.Name,
						Type:    op.RDType,
						RDClass: op.RDClass,
						TTL:     ttl,
						Records: op.Records,
					})
				}
				ts := ev.Timestamp
				_, recErr := e.Store.RecordExternalChange(ctx, store.RecordExternalOpts{
					ChangeID:    deref(ev.ChangeID),
					Name:        ev.DefaultName(),
					Zone:        zone,
					Operations:  opsStore,
					Status:      status,
					Actor:       ev.Actor,
					Trigger:     ev.Trigger,
					ResultRcode: ev.Rcode,
					Error:       ev.Error,
					OccurredAt:  &ts,
					Source:      "manual",
				})
				if recErr != nil {
					metrics.IncWebhookAutorecordedChanges("failed")
					logging.LogInternalEvent(e.Log, "webhook_autorecord_failed", slog.LevelWarn,
						slog.String("zone", zone),
						slog.String("change_id", deref(ev.ChangeID)),
						slog.String("error", recErr.Error()),
					)
					ev.ChangeID = nil
					ev.Autorecord = false
				} else {
					metrics.IncWebhookAutorecordedChanges("recorded")
					logging.LogInternalEvent(e.Log, "webhook_autorecorded_change", slog.LevelInfo,
						slog.String("zone", zone),
						slog.String("change_id", deref(ev.ChangeID)),
						slog.String("trigger", ev.Trigger),
					)
					ev.Autorecord = false
				}
			}
		}

		body, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		for _, t := range targets {
			id := uuid.NewString()
			if err := e.Store.OutboxEnqueue(ctx, id, body, t.Name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logging.LogInternalEvent(e.Log, "webhook_emit_failed", slog.LevelWarn,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
	}
	return err
}

func stringsHasSuffixDot(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '.'
}
