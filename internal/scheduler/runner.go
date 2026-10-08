package scheduler

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func leaseOwnerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return host + ":" + uuid.NewString()[:8]
}

// RunLoop polls for due scheduled changes and executes them until ctx is cancelled.
//
// Retention runs only on idle ticks (no due change claimed) so a vacuum never
// delays applying a scheduled change.
//
// broadcastLive, when non-nil, is invoked after successful record applies so
// WebSocket clients get ops without waiting on NOTIFY→IXFR.
func RunLoop(
	ctx context.Context,
	st *store.Store,
	client *dnsx.Client,
	cache *dnsx.ZoneCache,
	schedSettings config.SchedulerSettings,
	retentionSettings config.RetentionSettings,
	prov ZoneProvisioner,
	broadcastLive func(zone string, ops []dnsx.Operation),
) {
	owner := leaseOwnerID()
	pollInterval := schedSettings.PollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	lastRetentionAt := time.Now()

	logging.LogInternalEvent(nil, "scheduler_started", slog.LevelInfo,
		slog.String("lease_owner", owner),
		slog.Duration("poll_interval", pollInterval),
		slog.String("database_backend", st.Backend()),
		slog.Bool("retention_enabled", retentionSettings.Enabled),
		slog.Duration("retention_interval", retentionSettings.Interval),
	)

	defer logging.LogInternalEvent(nil, "scheduler_stopped", slog.LevelInfo,
		slog.String("lease_owner", owner),
	)

	for {
		idle := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					logging.LogInternalEvent(nil, "scheduler_tick_error", slog.LevelError,
						slog.Any("error", r),
					)
				}
			}()

			expired, err := st.ExpireOverdue(ctx, nil)
			if err != nil {
				logging.LogInternalEvent(nil, "scheduler_tick_error", slog.LevelError,
					slog.String("error", err.Error()),
				)
			} else {
				for _, changeID := range expired {
					metrics.IncScheduledChangesExpired()
					logging.LogInternalEvent(nil, "scheduled_change_expired", slog.LevelInfo,
						slog.String("change_id", changeID),
					)
				}
			}

			pending, err := st.CountPending(ctx)
			if err == nil {
				metrics.SetScheduledChangesPending(float64(pending))
			}

			change, err := st.ClaimDue(ctx, owner, schedSettings.LeaseTTL)
			if err != nil {
				logging.LogInternalEvent(nil, "scheduler_tick_error", slog.LevelError,
					slog.String("error", err.Error()),
				)
				return
			}
			if change != nil {
				idle = false
				logging.LogInternalEvent(nil, "scheduled_change_claimed", slog.LevelInfo,
					slog.String("change_id", change.ID),
					slog.String("zone", change.Zone),
					slog.String("name", change.Name),
					slog.Int("attempt", change.Attempts),
				)

				result := ExecuteChange(ctx, change, st, client, cache, ExecuteOpts{
					MaxAttempts:   schedSettings.MaxAttempts,
					RetryBackoff:  schedSettings.RetryBackoff,
					Actor:         &owner,
					Trigger:       notifications.TriggerScheduler,
					Provisioner:   prov,
					BroadcastLive: broadcastLive,
				})

				if result.Success {
					attrs := []any{
						slog.String("change_id", change.ID),
						slog.String("zone", change.Zone),
					}
					if result.NewSerial != nil {
						attrs = append(attrs, slog.Int64("new_serial", *result.NewSerial))
					}
					logging.LogInternalEvent(nil, "scheduled_change_applied", slog.LevelInfo, attrs...)
				} else {
					attrs := []any{
						slog.String("change_id", change.ID),
						slog.String("zone", change.Zone),
						slog.String("error", result.Error),
					}
					if result.ResultRcode != nil {
						attrs = append(attrs, slog.String("result_rcode", *result.ResultRcode))
					}
					logging.LogInternalEvent(nil, "scheduled_change_failed", slog.LevelWarn, attrs...)
				}
				return
			}

			if retentionSettings.Enabled && idle {
				interval := retentionSettings.Interval
				if interval <= 0 {
					interval = time.Hour
				}
				if time.Since(lastRetentionAt) >= interval {
					if _, err := store.RunRetention(ctx, st, retentionSettings); err != nil {
						metrics.IncRetentionErrors()
						logging.LogInternalEvent(nil, "retention_pass_failed", slog.LevelError,
							slog.String("error", err.Error()),
						)
					}
					lastRetentionAt = time.Now()
				}
			}
		}()

		if !idle {
			// Process next change immediately if one was found.
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}
