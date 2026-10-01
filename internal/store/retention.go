package store

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

// RetentionResult summarizes one retention maintenance pass.
type RetentionResult struct {
	PurgedByAge           int
	PurgedBySize          int
	EventsPurged          int
	BytesBefore           int64
	BytesAfter            int64
	TrimPasses            int
	EligibleRemaining     int
	DurationMS            float64
	DryRun                bool
	Skipped               bool
	OverLimitNoCandidates bool
	ByStatus              map[string]int
}

// RunRetention runs one retention pass against the scheduled-change store.
func RunRetention(ctx context.Context, store *Store, settings config.RetentionSettings) (RetentionResult, error) {
	if !settings.Enabled {
		return RetentionResult{Skipped: true}, nil
	}

	now := time.Now().UTC()
	started := time.Now()
	result := RetentionResult{DryRun: settings.DryRun, ByStatus: map[string]int{}}
	statuses := settings.Statuses
	if len(statuses) == 0 {
		statuses = []string{StatusApplied, StatusFailed, StatusCancelled, StatusExpired, StatusReverted}
	}

	bytesBefore, err := store.DatabaseSizeBytes(ctx)
	if err != nil {
		return result, err
	}
	result.BytesBefore = bytesBefore
	metrics.SetRetentionDatabaseBytes(float64(bytesBefore))

	// Age-based purge
	if settings.MaxAgeDays > 0 {
		cutoff := now.AddDate(0, 0, -settings.MaxAgeDays)
		for {
			batchSize := 500
			if settings.DryRun {
				batchSize = 1_000_000
			}
			batch, err := store.PurgeByAge(ctx, cutoff, statuses, PurgeOpts{
				Now: &now, BatchSize: batchSize, DryRun: settings.DryRun,
			})
			if err != nil {
				return result, err
			}
			if batch.ChangesDeleted() == 0 {
				break
			}
			result.PurgedByAge += batch.ChangesDeleted()
			result.EventsPurged += batch.EventsDeleted
			recordPurge(batch, "age", result.ByStatus, settings.DryRun)
			if settings.DryRun {
				break
			}
		}
		if result.PurgedByAge > 0 && !settings.DryRun && settings.Vacuum != "off" {
			if err := store.ReclaimSpace(ctx, settings.Vacuum); err != nil {
				return result, err
			}
		}
	}

	// Size-based trim
	if settings.MaxDatabaseMB > 0 {
		limitBytes := int64(settings.MaxDatabaseMB) * 1024 * 1024
		size, err := store.DatabaseSizeBytes(ctx)
		if err != nil {
			return result, err
		}
		maxPasses := settings.MaxTrimPasses
		if maxPasses <= 0 {
			maxPasses = 10
		}
		trimPercent := settings.TrimPercent
		if trimPercent <= 0 {
			trimPercent = 10
		}
		for size > limitBytes && result.TrimPasses < maxPasses {
			eligible, err := store.CountPurgeable(ctx, statuses, &now, nil)
			if err != nil {
				return result, err
			}
			if eligible <= 0 {
				result.OverLimitNoCandidates = true
				slog.Warn("retention_over_limit_no_candidates",
					"database_bytes", size, "limit_bytes", limitBytes, "vacuum", settings.Vacuum)
				break
			}
			toDelete := int(math.Ceil(float64(eligible) * float64(trimPercent) / 100))
			if toDelete < 1 {
				toDelete = 1
			}
			batch, err := store.PurgeOldest(ctx, toDelete, statuses, PurgeOpts{
				Now: &now, DryRun: settings.DryRun,
			})
			if err != nil {
				return result, err
			}
			if batch.ChangesDeleted() == 0 {
				break
			}
			result.TrimPasses++
			result.PurgedBySize += batch.ChangesDeleted()
			result.EventsPurged += batch.EventsDeleted
			recordPurge(batch, "size", result.ByStatus, settings.DryRun)

			if settings.DryRun {
				break
			}
			if settings.Vacuum == "off" {
				slog.Warn("retention_vacuum_disabled",
					"message", "Size cap cannot shrink the database file while vacuum is off",
					"database_bytes", size, "limit_bytes", limitBytes)
				break
			}
			if err := store.ReclaimSpace(ctx, settings.Vacuum); err != nil {
				return result, err
			}
			size, err = store.DatabaseSizeBytes(ctx)
			if err != nil {
				return result, err
			}
		}
	}

	bytesAfter, err := store.DatabaseSizeBytes(ctx)
	if err != nil {
		return result, err
	}
	result.BytesAfter = bytesAfter
	metrics.SetRetentionDatabaseBytes(float64(bytesAfter))

	eligible, err := store.CountPurgeable(ctx, statuses, &now, nil)
	if err != nil {
		return result, err
	}
	result.EligibleRemaining = eligible
	result.DurationMS = float64(time.Since(started).Milliseconds())
	metrics.ObserveRetentionDuration(result.DurationMS / 1000.0)
	if !settings.DryRun {
		metrics.SetRetentionLastSuccessTimestamp(float64(now.Unix()))
	}

	slog.Info("retention_pass_completed",
		"purged_by_age", result.PurgedByAge,
		"purged_by_size", result.PurgedBySize,
		"events_purged", result.EventsPurged,
		"bytes_before", result.BytesBefore,
		"bytes_after", result.BytesAfter,
		"trim_passes", result.TrimPasses,
		"eligible_remaining", result.EligibleRemaining,
		"duration_ms", result.DurationMS,
		"dry_run", result.DryRun,
		"over_limit_no_candidates", result.OverLimitNoCandidates,
	)
	return result, nil
}

func recordPurge(batch PurgeBatchResult, reason string, totals map[string]int, dryRun bool) {
	for status, count := range batch.ByStatus {
		totals[status] += count
		if !dryRun && count > 0 {
			metrics.IncRetentionChangesPurged(reason, status)
		}
	}
	if !dryRun && batch.EventsDeleted > 0 {
		for i := 0; i < batch.EventsDeleted; i++ {
			metrics.IncRetentionEventsPurged(reason)
		}
	}
}
