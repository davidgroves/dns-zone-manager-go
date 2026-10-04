package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

type txKey struct{}

// Store persists scheduled DNS changes and the webhook outbox.
type Store struct {
	settings               config.DatabaseSettings
	defaultExpiryWindow    time.Duration
	db                     *bun.DB
	backend                string
	ownsDB                 bool
	writeMu                sync.Mutex
	sqliteIncrementalReady bool
	databasePath           string
}

// New creates a Store. Call Open before use.
func New(settings config.DatabaseSettings, defaultExpiryWindow time.Duration) *Store {
	if defaultExpiryWindow <= 0 {
		defaultExpiryWindow = time.Hour
	}
	s := &Store{
		settings:            settings,
		defaultExpiryWindow: defaultExpiryWindow,
		backend:             settings.Backend,
	}
	if s.backend == "" {
		s.backend = "sqlite"
	}
	if s.backend == "sqlite" {
		s.databasePath = settings.Path
	}
	return s
}

// Open connects and migrates (when auto_migrate is enabled).
func (s *Store) Open(ctx context.Context) error {
	if s.db == nil {
		db, backend, err := Open(s.settings)
		if err != nil {
			return err
		}
		s.db = db
		s.backend = backend
		s.ownsDB = true
		if backend == "sqlite" {
			s.databasePath = s.settings.Path
			restrictSQLitePermissions(s.databasePath)
		}
	}

	if s.settings.AutoMigrate {
		if err := Upgrade(ctx, s.db, s.backend); err != nil {
			return err
		}
	} else {
		ok, err := SchemaExists(ctx, s.db, s.backend)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("store: no scheduled change schema found and database.auto_migrate is disabled")
		}
	}
	return nil
}

// Close releases the database if this store opened it.
func (s *Store) Close() error {
	if s.db != nil && s.ownsDB {
		err := s.db.Close()
		s.db = nil
		return err
	}
	s.db = nil
	return nil
}

// Ping checks the database answers.
func (s *Store) Ping(ctx context.Context) bool {
	if s.db == nil {
		return false
	}
	return s.db.PingContext(ctx) == nil
}

// Backend returns the configured backend name.
func (s *Store) Backend() string { return s.backend }

// DB returns the underlying bun.DB (for tests / advanced use).
func (s *Store) DB() *bun.DB { return s.db }

func (s *Store) requireDB() (*bun.DB, error) {
	if s.db == nil {
		return nil, errors.New("store: not open")
	}
	return s.db, nil
}

func (s *Store) isPostgres() bool { return s.backend == "postgres" }

// RunInTx runs fn inside a store transaction. Nested RunInTx / store methods
// reuse the same transaction when ctx already carries one.
func (s *Store) RunInTx(ctx context.Context, operation string, fn func(ctx context.Context) error) error {
	return s.runTx(ctx, operation, func(ctx context.Context, _ bun.Tx) error {
		return fn(ctx)
	})
}

func (s *Store) runTx(ctx context.Context, operation string, fn func(ctx context.Context, tx bun.Tx) error) error {
	if existing, ok := ctx.Value(txKey{}).(bun.Tx); ok {
		return fn(ctx, existing)
	}
	db, err := s.requireDB()
	if err != nil {
		return err
	}

	started := time.Now()
	var runErr error
	defer func() {
		metrics.ObserveStoreOperation(operation, s.backend, time.Since(started).Seconds())
		if runErr != nil && !isBusinessError(runErr) {
			metrics.IncStoreErrors(operation, s.backend)
		}
	}()

	if s.backend == "sqlite" {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
	}

	runErr = db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		ctx = context.WithValue(ctx, txKey{}, tx)
		return fn(ctx, tx)
	})
	return runErr
}

func isBusinessError(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidStatus) || errors.Is(err, ErrValidation)
}

var (
	ErrNotFound      = errors.New("store: not found")
	ErrInvalidStatus = errors.New("store: invalid status")
	ErrValidation    = errors.New("store: validation")
)

// AddEvent appends an audit event for a change.
func (s *Store) AddEvent(ctx context.Context, changeID, event string, actor *string, detail map[string]any) error {
	return s.runTx(ctx, "add_event", func(ctx context.Context, tx bun.Tx) error {
		return s.addEventTx(ctx, tx, changeID, event, actor, detail, time.Now().UTC())
	})
}

func (s *Store) addEventTx(ctx context.Context, tx bun.IDB, changeID, event string, actor *string, detail map[string]any, ts time.Time) error {
	var detailJSON JSONText
	var err error
	if detail != nil {
		detailJSON, err = MarshalJSON(detail)
		if err != nil {
			return err
		}
	}
	_, err = tx.NewInsert().Model(&ScheduledChangeEventRow{
		ChangeID: changeID,
		TS:       NewUtcTime(ts),
		Event:    event,
		Actor:    actor,
		Detail:   detailJSON,
	}).Exec(ctx)
	return err
}

// Create creates a new scheduled change.
func (s *Store) Create(ctx context.Context, data ChangeCreateData) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "create", func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		changeID := uuid.NewString()
		status := StatusDraft
		if data.ScheduledAt != nil {
			status = StatusScheduled
		}
		notValidAfter := data.NotValidAfter
		if notValidAfter == nil && data.ScheduledAt != nil {
			nv := data.ScheduledAt.Add(s.defaultExpiryWindow)
			notValidAfter = &nv
		}
		auto := data.AutoPrerequisites
		kind := data.Kind
		if kind == "" {
			kind = KindRecords
		}
		row := &ScheduledChangeRow{
			ID:                changeID,
			Name:              data.Name,
			Description:       data.Description,
			Zone:              data.Zone,
			Status:            status,
			AutoPrerequisites: auto,
			CreatedAt:         NewUtcTime(now),
			CreatedBy:         data.CreatedBy,
			UpdatedAt:         NewUtcTime(now),
			Attempts:          0,
			Source:            "scheduler",
			Kind:              kind,
			Payload:           JSONText(data.Payload),
		}
		if data.ScheduledAt != nil {
			t := NewUtcTime(*data.ScheduledAt)
			row.ScheduledAt = &t
		}
		if notValidAfter != nil {
			t := NewUtcTime(*notValidAfter)
			row.NotValidAfter = &t
		}
		if _, err := tx.NewInsert().Model(row).Exec(ctx); err != nil {
			return err
		}
		if err := s.insertOperations(ctx, tx, changeID, data.Operations); err != nil {
			return err
		}
		if err := s.insertPrerequisites(ctx, tx, changeID, data.Prerequisites); err != nil {
			return err
		}
		detail := map[string]any{
			"status":              status,
			"zone":                data.Zone,
			"operations_count":    len(data.Operations),
			"prerequisites_count": len(data.Prerequisites),
			"auto_prerequisites":  auto,
		}
		if data.ScheduledAt != nil {
			detail["scheduled_at"] = FormatISO(*data.ScheduledAt)
		} else {
			detail["scheduled_at"] = nil
		}
		if err := s.addEventTx(ctx, tx, changeID, "created", data.CreatedBy, detail, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// RecordExternalChange records a change already executed outside the scheduler.
func (s *Store) RecordExternalChange(ctx context.Context, opts RecordExternalOpts) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "record_external_change", func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		if opts.OccurredAt != nil {
			now = opts.OccurredAt.UTC()
		}
		changeID := opts.ChangeID
		if changeID == "" {
			changeID = uuid.NewString()
		}
		zone := opts.Zone
		if !strings.HasSuffix(zone, ".") {
			zone += "."
		}
		status := opts.Status
		if status == "" {
			status = StatusApplied
		}
		source := opts.Source
		if source == "" {
			source = "manual"
		}
		trigger := opts.Trigger
		if trigger == "" {
			trigger = "manual"
		}
		name := opts.Name
		if len(name) > 200 {
			name = name[:200]
		}
		ops := make([]AtomicOperation, 0, len(opts.Operations))
		for _, op := range opts.Operations {
			a := AtomicOperation{
				Action:  op.Action,
				Name:    op.Name,
				Type:    op.Type,
				RDClass: op.RDClass,
				TTL:     op.TTL,
				Records: op.Records,
			}
			if a.RDClass == "" {
				a.RDClass = "IN"
			}
			if a.TTL == 0 {
				a.TTL = 3600
			}
			ops = append(ops, a)
		}
		kind := opts.Kind
		if kind == "" {
			kind = KindRecords
		}
		row := &ScheduledChangeRow{
			ID:                changeID,
			Name:              name,
			Zone:              zone,
			Status:            status,
			AutoPrerequisites: false,
			CreatedAt:         NewUtcTime(now),
			CreatedBy:         opts.Actor,
			UpdatedAt:         NewUtcTime(now),
			Attempts:          0,
			ResultRcode:       opts.ResultRcode,
			LastError:         opts.Error,
			Source:            source,
			Kind:              kind,
			Payload:           JSONText(opts.Payload),
		}
		if status == StatusApplied {
			t := NewUtcTime(now)
			row.AppliedAt = &t
		}
		if _, err := tx.NewInsert().Model(row).Exec(ctx); err != nil {
			return err
		}
		if err := s.insertOperations(ctx, tx, changeID, ops); err != nil {
			return err
		}
		detail := map[string]any{
			"trigger":          trigger,
			"source":           source,
			"zone":             zone,
			"operations_count": len(ops),
			"result_rcode":     opts.ResultRcode,
			"error":            opts.Error,
		}
		second := "failed"
		if status == StatusApplied {
			second = "applied"
		}
		for _, ev := range []string{"created", second} {
			if err := s.addEventTx(ctx, tx, changeID, ev, opts.Actor, detail, now); err != nil {
				return err
			}
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// RecordExternalOpts configures RecordExternalChange.
type RecordExternalOpts struct {
	Name        string
	Zone        string
	Operations  []AtomicOperation
	ChangeID    string
	Status      string
	Actor       *string
	Trigger     string
	ResultRcode *string
	Error       *string
	OccurredAt  *time.Time
	Source      string
	Kind        string
	Payload     json.RawMessage
}

func (s *Store) insertOperations(ctx context.Context, tx bun.IDB, changeID string, operations []AtomicOperation) error {
	if len(operations) == 0 {
		return nil
	}
	rows := make([]ScheduledOperationRow, 0, len(operations))
	for seq, op := range operations {
		rdclass := op.RDClass
		if rdclass == "" {
			rdclass = "IN"
		}
		ttl := op.TTL
		if ttl == 0 {
			ttl = 3600
		}
		var records JSONText
		if op.Records != nil {
			b, err := MarshalJSON(op.Records)
			if err != nil {
				return err
			}
			records = b
		}
		rows = append(rows, ScheduledOperationRow{
			ChangeID: changeID,
			Seq:      seq,
			Action:   op.Action,
			Name:     op.Name,
			Type:     op.Type,
			RDClass:  rdclass,
			TTL:      ttl,
			Records:  records,
		})
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return err
}

func (s *Store) insertPrerequisites(ctx context.Context, tx bun.IDB, changeID string, prereqs []ChangePrerequisite) error {
	if len(prereqs) == 0 {
		return nil
	}
	rows := make([]ScheduledPrerequisiteRow, 0, len(prereqs))
	for seq, p := range prereqs {
		rdclass := p.RDClass
		if rdclass == "" {
			rdclass = "IN"
		}
		rows = append(rows, ScheduledPrerequisiteRow{
			ChangeID:   changeID,
			Seq:        seq,
			PrereqType: p.PrereqType,
			Name:       p.Name,
			RDType:     p.RDType,
			RDClass:    rdclass,
			Data:       p.Data,
		})
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return err
}

// Get fetches a change by ID.
func (s *Store) Get(ctx context.Context, changeID string, includeEvents bool) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "get", func(ctx context.Context, tx bun.Tx) error {
		ch, err := s.getTx(ctx, tx, changeID, includeEvents)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

func (s *Store) getTx(ctx context.Context, tx bun.IDB, changeID string, includeEvents bool) (*ScheduledChange, error) {
	var row ScheduledChangeRow
	err := tx.NewSelect().Model(&row).Where("id = ?", changeID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.rowToChange(ctx, tx, &row, includeEvents)
}

// ListChanges lists changes with optional filters.
func (s *Store) ListChanges(ctx context.Context, opts ListChangesOpts) ([]*ScheduledChange, error) {
	var out []*ScheduledChange
	err := s.runTx(ctx, "list_changes", func(ctx context.Context, tx bun.Tx) error {
		q := tx.NewSelect().Model((*ScheduledChangeRow)(nil))
		statuses := opts.Statuses
		if len(statuses) == 0 && opts.Status != "" {
			statuses = []string{opts.Status}
		}
		if len(statuses) > 0 {
			q = q.Where("status IN (?)", bun.In(statuses))
		}
		zone := opts.Zone
		if zone != "" {
			if !strings.HasSuffix(zone, ".") {
				zone += "."
			}
			q = q.Where("zone = ?", zone)
		}
		if opts.Source != "" {
			q = q.Where("COALESCE(source, 'scheduler') = ?", opts.Source)
		}
		q = q.OrderExpr("COALESCE(scheduled_at, created_at) DESC")
		var rows []ScheduledChangeRow
		if err := q.Scan(ctx, &rows); err != nil {
			return err
		}
		out = make([]*ScheduledChange, 0, len(rows))
		for i := range rows {
			ch, err := s.rowToChange(ctx, tx, &rows[i], opts.IncludeEvents)
			if err != nil {
				return err
			}
			out = append(out, ch)
		}
		return nil
	})
	return out, err
}

// ListChangesOpts filters ListChanges.
type ListChangesOpts struct {
	Status        string
	Statuses      []string
	Zone          string
	Source        string
	IncludeEvents bool
}

// Update updates an editable change.
func (s *Store) Update(ctx context.Context, changeID string, data ChangeUpdateData) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "update", func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.getTx(ctx, tx, changeID, false)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: change %s", ErrNotFound, changeID)
		}
		if _, ok := EditableStatuses[existing.Status]; !ok {
			return fmt.Errorf("%w: cannot edit change in status %q", ErrInvalidStatus, existing.Status)
		}
		if data.Operations != nil && len(data.Operations) == 0 {
			return fmt.Errorf("%w: a change must have at least one operation", ErrValidation)
		}

		now := time.Now().UTC()
		changes := map[string]any{}
		row := map[string]any{"updated_at": FormatISO(now)}

		if data.Name != nil {
			row["name"] = *data.Name
			if *data.Name != existing.Name {
				changes["name"] = map[string]any{"from": existing.Name, "to": *data.Name}
			}
		}

		newDesc := existing.Description
		if data.ClearDescription {
			newDesc = nil
			row["description"] = nil
		} else if data.Description != nil {
			newDesc = data.Description
			row["description"] = *data.Description
		}
		if !ptrEqual(newDesc, existing.Description) {
			changes["description"] = map[string]any{"from": existing.Description, "to": newDesc}
		}

		if data.AutoPrerequisites != nil {
			newAuto := *data.AutoPrerequisites
			row["auto_prerequisites"] = newAuto
			if newAuto != existing.AutoPrerequisites {
				changes["auto_prerequisites"] = map[string]any{
					"from": existing.AutoPrerequisites, "to": newAuto,
				}
			}
		}

		scheduledAt := existing.ScheduledAt
		if data.ClearScheduledAt {
			scheduledAt = nil
			row["scheduled_at"] = nil
		} else if data.ScheduledAt != nil {
			scheduledAt = data.ScheduledAt
			row["scheduled_at"] = FormatISO(*data.ScheduledAt)
		}
		if !timePtrEqual(scheduledAt, existing.ScheduledAt) {
			changes["scheduled_at"] = map[string]any{
				"from": FormatISOPtr(existing.ScheduledAt),
				"to":   FormatISOPtr(scheduledAt),
			}
		}

		notValidAfter := existing.NotValidAfter
		if data.ClearNotValidAfter {
			notValidAfter = nil
			row["not_valid_after"] = nil
		} else if data.NotValidAfter != nil {
			notValidAfter = data.NotValidAfter
			row["not_valid_after"] = FormatISO(*data.NotValidAfter)
		} else if data.ScheduledAt != nil && existing.NotValidAfter == nil {
			nv := data.ScheduledAt.Add(s.defaultExpiryWindow)
			notValidAfter = &nv
			row["not_valid_after"] = FormatISO(nv)
		}
		if !timePtrEqual(notValidAfter, existing.NotValidAfter) {
			changes["not_valid_after"] = map[string]any{
				"from": FormatISOPtr(existing.NotValidAfter),
				"to":   FormatISOPtr(notValidAfter),
			}
		}

		newStatus := StatusDraft
		if scheduledAt != nil {
			newStatus = StatusScheduled
		}
		if existing.Status == StatusFailed && scheduledAt != nil {
			newStatus = StatusScheduled
			row["last_error"] = nil
			row["next_attempt_at"] = nil
		}
		row["status"] = newStatus
		if newStatus != existing.Status {
			changes["status"] = map[string]any{"from": existing.Status, "to": newStatus}
		}

		if data.Operations != nil {
			before := serializeOps(existing.Operations)
			after := serializeAtomic(data.Operations)
			if !jsonEqual(before, after) {
				changes["operations"] = map[string]any{
					"from_count": len(before), "to_count": len(after),
					"from": before, "to": after,
				}
			}
		}
		if data.HasPrerequisites {
			before := serializePrereqs(existing.Prerequisites)
			after := serializePrereqs(data.Prerequisites)
			if !jsonEqual(before, after) {
				changes["prerequisites"] = map[string]any{
					"from_count": len(before), "to_count": len(after),
					"from": before, "to": after,
				}
			}
		}

		uq := tx.NewUpdate().Table("scheduled_changes").Where("id = ?", changeID)
		for k, v := range row {
			if v == nil {
				uq = uq.Set("? = NULL", bun.Ident(k))
			} else {
				uq = uq.Set("? = ?", bun.Ident(k), v)
			}
		}
		if _, err := uq.Exec(ctx); err != nil {
			return err
		}

		if data.Operations != nil {
			if _, err := tx.NewDelete().Model((*ScheduledOperationRow)(nil)).
				Where("change_id = ?", changeID).Exec(ctx); err != nil {
				return err
			}
			if err := s.insertOperations(ctx, tx, changeID, data.Operations); err != nil {
				return err
			}
		}
		if data.HasPrerequisites {
			if _, err := tx.NewDelete().Model((*ScheduledPrerequisiteRow)(nil)).
				Where("change_id = ?", changeID).Exec(ctx); err != nil {
				return err
			}
			if err := s.insertPrerequisites(ctx, tx, changeID, data.Prerequisites); err != nil {
				return err
			}
		}

		if err := s.addEventTx(ctx, tx, changeID, "updated", data.Actor, map[string]any{
			"status": newStatus, "changes": changes,
		}, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

func ptrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func serializeOps(ops []ScheduledOperation) []map[string]any {
	out := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		out = append(out, map[string]any{
			"action": op.Action, "name": op.Name, "type": op.Type,
			"rdclass": op.RDClass, "ttl": op.TTL, "records": op.Records,
		})
	}
	return out
}

func serializeAtomic(ops []AtomicOperation) []map[string]any {
	out := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		rdclass := op.RDClass
		if rdclass == "" {
			rdclass = "IN"
		}
		ttl := op.TTL
		if ttl == 0 {
			ttl = 3600
		}
		var records any
		if op.Records != nil {
			records = op.Records
		}
		out = append(out, map[string]any{
			"action": op.Action, "name": op.Name, "type": op.Type,
			"rdclass": rdclass, "ttl": ttl, "records": records,
		})
	}
	return out
}

func serializePrereqs(ps []ChangePrerequisite) []map[string]any {
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, map[string]any{
			"prereq_type": p.PrereqType, "name": p.Name,
			"rdtype": p.RDType, "rdclass": p.RDClass, "data": p.Data,
		})
	}
	return out
}

// Cancel cancels a pending change.
func (s *Store) Cancel(ctx context.Context, changeID string, actor *string) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "cancel", func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.getTx(ctx, tx, changeID, false)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: change %s", ErrNotFound, changeID)
		}
		if _, ok := CancellableStatuses[existing.Status]; !ok {
			return fmt.Errorf("%w: cannot cancel change in status %q", ErrInvalidStatus, existing.Status)
		}
		now := time.Now().UTC()
		if _, err := tx.NewUpdate().Model((*ScheduledChangeRow)(nil)).
			Where("id = ?", changeID).
			Set("status = ?", StatusCancelled).
			Set("updated_at = ?", FormatISO(now)).
			Set("lease_owner = NULL").
			Set("lease_expires_at = NULL").
			Exec(ctx); err != nil {
			return err
		}
		if err := s.addEventTx(ctx, tx, changeID, "cancelled", actor, map[string]any{
			"previous_status": existing.Status,
		}, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// ClaimDue atomically claims the next due scheduled change.
func (s *Store) ClaimDue(ctx context.Context, leaseOwner string, leaseTTL time.Duration) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "claim_due", func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		if _, err := s.expireOverdueTx(ctx, tx, now); err != nil {
			return err
		}
		nowISO := FormatISO(now)
		leaseExpires := FormatISO(now.Add(leaseTTL))

		var candidateID string
		q := `
			SELECT id FROM scheduled_changes
			WHERE scheduled_at IS NOT NULL
			  AND scheduled_at <= ?
			  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			  AND (
			    (status = 'scheduled' AND (lease_expires_at IS NULL OR lease_expires_at < ?))
			    OR
			    (status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at < ?)
			  )
			ORDER BY scheduled_at
			LIMIT 1`
		if s.isPostgres() {
			q += " FOR UPDATE SKIP LOCKED"
		}
		err := tx.NewRaw(q, nowISO, nowISO, nowISO, nowISO).Scan(ctx, &candidateID)
		if errors.Is(err, sql.ErrNoRows) || candidateID == "" {
			return nil
		}
		if err != nil {
			return err
		}

		res, err := tx.NewUpdate().Model((*ScheduledChangeRow)(nil)).
			Where("id = ?", candidateID).
			Set("status = ?", StatusRunning).
			Set("lease_owner = ?", leaseOwner).
			Set("lease_expires_at = ?", leaseExpires).
			Set("attempts = attempts + 1").
			Set("updated_at = ?", nowISO).
			Exec(ctx)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		ch, err := s.getTx(ctx, tx, candidateID, true)
		if err != nil {
			return err
		}
		if ch != nil {
			if err := s.addEventTx(ctx, tx, ch.ID, "claimed", &leaseOwner, map[string]any{
				"attempt": ch.Attempts,
			}, now); err != nil {
				return err
			}
			// Reload so events include claimed
			ch, err = s.getTx(ctx, tx, candidateID, true)
			if err != nil {
				return err
			}
		}
		out = ch
		return nil
	})
	return out, err
}

// MarkApplied marks a change as successfully applied.
func (s *Store) MarkApplied(ctx context.Context, changeID string, opts MarkAppliedOpts) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "mark_applied", func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		trigger := opts.Trigger
		if trigger == "" {
			trigger = "scheduler"
		}
		uq := tx.NewUpdate().Model((*ScheduledChangeRow)(nil)).
			Where("id = ?", changeID).
			Set("status = ?", StatusApplied).
			Set("applied_at = ?", FormatISO(now)).
			Set("last_error = NULL").
			Set("next_attempt_at = NULL").
			Set("lease_owner = NULL").
			Set("lease_expires_at = NULL").
			Set("updated_at = ?", FormatISO(now))
		if opts.ResultRcode != nil {
			uq = uq.Set("result_rcode = ?", *opts.ResultRcode)
		} else {
			uq = uq.Set("result_rcode = NULL")
		}
		if opts.NewSerial != nil {
			uq = uq.Set("new_serial = ?", *opts.NewSerial)
		} else {
			uq = uq.Set("new_serial = NULL")
		}
		if _, err := uq.Exec(ctx); err != nil {
			return err
		}
		if err := s.addEventTx(ctx, tx, changeID, "applied", opts.Actor, map[string]any{
			"trigger": trigger, "result_rcode": opts.ResultRcode, "new_serial": opts.NewSerial,
		}, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// MarkAppliedOpts configures MarkApplied.
type MarkAppliedOpts struct {
	ResultRcode *string
	NewSerial   *int64
	Actor       *string
	Trigger     string
}

// SaveOperationSnapshots persists pre-apply RRset snapshots.
func (s *Store) SaveOperationSnapshots(ctx context.Context, changeID string, snapshots []OpSnapshot) error {
	return s.runTx(ctx, "save_operation_snapshots", func(ctx context.Context, tx bun.Tx) error {
		for _, snap := range snapshots {
			var prior JSONText
			if snap.PriorRecords != nil {
				b, err := MarshalJSON(snap.PriorRecords)
				if err != nil {
					return err
				}
				prior = b
			}
			uq := tx.NewUpdate().Model((*ScheduledOperationRow)(nil)).
				Where("change_id = ? AND seq = ?", changeID, snap.Seq).
				Set("snapshot_at = ?", FormatISO(snap.SnapshotAt)).
				Set("prior_records = ?", prior)
			if snap.PriorTTL != nil {
				uq = uq.Set("prior_ttl = ?", *snap.PriorTTL)
			} else {
				uq = uq.Set("prior_ttl = NULL")
			}
			if _, err := uq.Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkReverted marks an applied change as reverted.
func (s *Store) MarkReverted(ctx context.Context, changeID string, opts MarkRevertedOpts) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "mark_reverted", func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.getTx(ctx, tx, changeID, false)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: change %s", ErrNotFound, changeID)
		}
		if existing.Status != StatusApplied {
			return fmt.Errorf("%w: cannot revert change in status %q", ErrInvalidStatus, existing.Status)
		}
		now := time.Now().UTC()
		uq := tx.NewUpdate().Model((*ScheduledChangeRow)(nil)).
			Where("id = ?", changeID).
			Set("status = ?", StatusReverted).
			Set("reverted_at = ?", FormatISO(now)).
			Set("last_error = NULL").
			Set("lease_owner = NULL").
			Set("lease_expires_at = NULL").
			Set("updated_at = ?", FormatISO(now))
		if opts.ResultRcode != nil {
			uq = uq.Set("result_rcode = ?", *opts.ResultRcode)
		}
		if opts.NewSerial != nil {
			uq = uq.Set("new_serial = ?", *opts.NewSerial)
		}
		if _, err := uq.Exec(ctx); err != nil {
			return err
		}
		if err := s.addEventTx(ctx, tx, changeID, "reverted", opts.Actor, map[string]any{
			"result_rcode": opts.ResultRcode, "new_serial": opts.NewSerial,
			"operations_count": opts.OperationsCount,
		}, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// MarkRevertedOpts configures MarkReverted.
type MarkRevertedOpts struct {
	ResultRcode     *string
	NewSerial       *int64
	Actor           *string
	OperationsCount int
}

// MarkFailed records a failure; reschedules for retry when eligible.
func (s *Store) MarkFailed(ctx context.Context, changeID, errMsg string, opts MarkFailedOpts) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "mark_failed", func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.getTx(ctx, tx, changeID, false)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: change %s", ErrNotFound, changeID)
		}
		now := time.Now().UTC()
		canRetry := existing.Attempts < opts.MaxAttempts && existing.ScheduledAt != nil
		if existing.Attempts < opts.MaxAttempts && existing.Status == StatusRunning {
			canRetry = existing.Attempts < opts.MaxAttempts
		}

		var nextStatus string
		var nextAttempt *time.Time
		if canRetry && existing.ScheduledAt != nil {
			nextStatus = StatusScheduled
			na := now.Add(opts.RetryBackoff * time.Duration(existing.Attempts))
			nextAttempt = &na
		} else {
			nextStatus = StatusFailed
		}

		uq := tx.NewUpdate().Model((*ScheduledChangeRow)(nil)).
			Where("id = ?", changeID).
			Set("status = ?", nextStatus).
			Set("last_error = ?", errMsg).
			Set("lease_owner = NULL").
			Set("lease_expires_at = NULL").
			Set("updated_at = ?", FormatISO(now))
		if opts.ResultRcode != nil {
			uq = uq.Set("result_rcode = ?", *opts.ResultRcode)
		} else {
			uq = uq.Set("result_rcode = NULL")
		}
		if nextAttempt != nil {
			uq = uq.Set("next_attempt_at = ?", FormatISO(*nextAttempt))
		} else {
			uq = uq.Set("next_attempt_at = NULL")
		}
		if _, err := uq.Exec(ctx); err != nil {
			return err
		}
		if err := s.addEventTx(ctx, tx, changeID, "failed", opts.Actor, map[string]any{
			"error": errMsg, "result_rcode": opts.ResultRcode,
			"next_status": nextStatus, "next_attempt_at": FormatISOPtr(nextAttempt),
		}, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// MarkFailedOpts configures MarkFailed.
type MarkFailedOpts struct {
	MaxAttempts  int
	RetryBackoff time.Duration
	Actor        *string
	ResultRcode  *string
}

// MarkRunning marks a change as running for apply-now.
func (s *Store) MarkRunning(ctx context.Context, changeID, leaseOwner string, leaseTTL time.Duration) (*ScheduledChange, error) {
	var out *ScheduledChange
	err := s.runTx(ctx, "mark_running", func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.getTx(ctx, tx, changeID, false)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: change %s", ErrNotFound, changeID)
		}
		if _, ok := EditableStatuses[existing.Status]; !ok {
			return fmt.Errorf("%w: cannot apply change in status %q", ErrInvalidStatus, existing.Status)
		}
		now := time.Now().UTC()
		leaseExpires := FormatISO(now.Add(leaseTTL))
		if _, err := tx.NewUpdate().Model((*ScheduledChangeRow)(nil)).
			Where("id = ?", changeID).
			Set("status = ?", StatusRunning).
			Set("lease_owner = ?", leaseOwner).
			Set("lease_expires_at = ?", leaseExpires).
			Set("attempts = attempts + 1").
			Set("updated_at = ?", FormatISO(now)).
			Exec(ctx); err != nil {
			return err
		}
		if err := s.addEventTx(ctx, tx, changeID, "apply_now", &leaseOwner, nil, now); err != nil {
			return err
		}
		ch, err := s.getTx(ctx, tx, changeID, true)
		if err != nil {
			return err
		}
		out = ch
		return nil
	})
	return out, err
}

// ExpireOverdue marks scheduled changes past not_valid_after as expired.
func (s *Store) ExpireOverdue(ctx context.Context, now *time.Time) ([]string, error) {
	var ids []string
	err := s.runTx(ctx, "expire_overdue", func(ctx context.Context, tx bun.Tx) error {
		t := time.Now().UTC()
		if now != nil {
			t = now.UTC()
		}
		var err error
		ids, err = s.expireOverdueTx(ctx, tx, t)
		return err
	})
	return ids, err
}

func (s *Store) expireOverdueTx(ctx context.Context, tx bun.IDB, now time.Time) ([]string, error) {
	nowISO := FormatISO(now)
	var ids []string
	err := tx.NewRaw(`
		UPDATE scheduled_changes
		SET status = 'expired', updated_at = ?, lease_owner = NULL, lease_expires_at = NULL
		WHERE status = 'scheduled'
		  AND not_valid_after IS NOT NULL
		  AND not_valid_after < ?
		RETURNING id`, nowISO, nowISO).Scan(ctx, &ids)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for _, id := range ids {
		if err := s.addEventTx(ctx, tx, id, "expired", strPtr("scheduler"), nil, now); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func strPtr(s string) *string { return &s }

// FindConflicts finds draft/scheduled/failed changes that touch the same name/type/class.
func (s *Store) FindConflicts(ctx context.Context, zone string, operations []AtomicOperation, excludeID string) ([]Conflict, error) {
	var out []Conflict
	err := s.runTx(ctx, "find_conflicts", func(ctx context.Context, tx bun.Tx) error {
		if !strings.HasSuffix(zone, ".") {
			zone += "."
		}
		pending, err := s.ListChanges(ctx, ListChangesOpts{Zone: zone, IncludeEvents: false})
		if err != nil {
			return err
		}
		targets := map[string]struct{}{}
		for _, op := range operations {
			key := strings.ToLower(strings.TrimSuffix(op.Name, ".")) + "|" + strings.ToUpper(op.Type) + "|" + strings.ToUpper(orIN(op.RDClass))
			targets[key] = struct{}{}
		}
		for _, other := range pending {
			if _, ok := ConflictStatuses[other.Status]; !ok {
				continue
			}
			if excludeID != "" && other.ID == excludeID {
				continue
			}
			for _, op := range other.Operations {
				key := strings.ToLower(strings.TrimSuffix(op.Name, ".")) + "|" + strings.ToUpper(op.Type) + "|" + strings.ToUpper(op.RDClass)
				if _, ok := targets[key]; ok {
					out = append(out, Conflict{
						OtherID: other.ID, OtherName: other.Name,
						Name: op.Name, Type: op.Type, RDClass: op.RDClass,
					})
				}
			}
		}
		return nil
	})
	return out, err
}

func orIN(s string) string {
	if s == "" {
		return "IN"
	}
	return s
}

// CountPending counts changes waiting to run.
func (s *Store) CountPending(ctx context.Context) (int, error) {
	var n int
	err := s.runTx(ctx, "count_pending", func(ctx context.Context, tx bun.Tx) error {
		statuses := sortedKeys(PendingStatuses)
		return tx.NewSelect().Model((*ScheduledChangeRow)(nil)).
			ColumnExpr("COUNT(*)").
			Where("status IN (?)", bun.In(statuses)).
			Scan(ctx, &n)
	})
	return n, err
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func completionTSExpr() string {
	return "COALESCE(reverted_at, applied_at, updated_at, created_at)"
}

func (s *Store) purgeableFilters(statuses []string, now time.Time, olderThan *time.Time) (string, []any) {
	nowISO := FormatISO(now)
	args := []any{bun.In(statuses), nowISO, nowISO}
	clause := `status IN (?)
		AND (scheduled_at IS NULL OR scheduled_at <= ?)
		AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`
	if olderThan != nil {
		clause += " AND " + completionTSExpr() + " < ?"
		args = append(args, FormatISO(*olderThan))
	}
	return clause, args
}

// DatabaseSizeBytes returns on-disk size for SQLite (file+wal+shm) or relation size for Postgres.
func (s *Store) DatabaseSizeBytes(ctx context.Context) (int64, error) {
	if s.backend == "sqlite" {
		if s.databasePath == "" {
			return 0, nil
		}
		var total int64
		for _, candidate := range []string{s.databasePath, s.databasePath + "-wal", s.databasePath + "-shm"} {
			fi, err := os.Stat(candidate)
			if err != nil {
				continue
			}
			total += fi.Size()
		}
		return total, nil
	}
	db, err := s.requireDB()
	if err != nil {
		return 0, err
	}
	tables := []string{
		"scheduled_changes", "scheduled_operations", "scheduled_prerequisites",
		"scheduled_change_events", "webhook_outbox",
	}
	var total int64
	for _, name := range tables {
		var size int64
		if err := db.NewRaw("SELECT pg_total_relation_size(?)", name).Scan(ctx, &size); err != nil {
			// table may not exist yet
			continue
		}
		total += size
	}
	return total, nil
}

// CountPurgeable counts changes eligible for retention purge.
func (s *Store) CountPurgeable(ctx context.Context, statuses []string, now *time.Time, olderThan *time.Time) (int, error) {
	var n int
	err := s.runTx(ctx, "count_purgeable", func(ctx context.Context, tx bun.Tx) error {
		t := time.Now().UTC()
		if now != nil {
			t = now.UTC()
		}
		clause, args := s.purgeableFilters(statuses, t, olderThan)
		return tx.NewRaw("SELECT COUNT(*) FROM scheduled_changes WHERE "+clause, args...).Scan(ctx, &n)
	})
	return n, err
}

// PurgeByAge deletes eligible changes older than cutoff.
func (s *Store) PurgeByAge(ctx context.Context, cutoff time.Time, statuses []string, opts PurgeOpts) (PurgeBatchResult, error) {
	var out PurgeBatchResult
	err := s.runTx(ctx, "purge_by_age", func(ctx context.Context, tx bun.Tx) error {
		t := time.Now().UTC()
		if opts.Now != nil {
			t = opts.Now.UTC()
		}
		batch := opts.BatchSize
		if batch <= 0 {
			batch = 500
		}
		clause, args := s.purgeableFilters(statuses, t, &cutoff)
		var res PurgeBatchResult
		var err error
		res, err = s.purgeMatching(ctx, tx, clause, args, batch, opts.DryRun)
		out = res
		return err
	})
	return out, err
}

// PurgeOldest deletes the oldest limit eligible changes.
func (s *Store) PurgeOldest(ctx context.Context, limit int, statuses []string, opts PurgeOpts) (PurgeBatchResult, error) {
	if limit <= 0 {
		return PurgeBatchResult{}, nil
	}
	var out PurgeBatchResult
	err := s.runTx(ctx, "purge_oldest", func(ctx context.Context, tx bun.Tx) error {
		t := time.Now().UTC()
		if opts.Now != nil {
			t = opts.Now.UTC()
		}
		clause, args := s.purgeableFilters(statuses, t, nil)
		var res PurgeBatchResult
		var err error
		res, err = s.purgeMatching(ctx, tx, clause, args, limit, opts.DryRun)
		out = res
		return err
	})
	return out, err
}

// PurgeOpts configures purge methods.
type PurgeOpts struct {
	Now       *time.Time
	BatchSize int
	DryRun    bool
}

func (s *Store) purgeMatching(ctx context.Context, tx bun.IDB, clause string, args []any, limit int, dryRun bool) (PurgeBatchResult, error) {
	type idStatus struct {
		ID     string `bun:"id"`
		Status string `bun:"status"`
	}
	queryArgs := append(append([]any{}, args...), limit)
	var rows []idStatus
	err := tx.NewRaw(
		"SELECT id, status FROM scheduled_changes WHERE "+clause+
			" ORDER BY "+completionTSExpr()+" ASC, id ASC LIMIT ?",
		queryArgs...,
	).Scan(ctx, &rows)
	if err != nil {
		return PurgeBatchResult{}, err
	}
	if len(rows) == 0 {
		return PurgeBatchResult{}, nil
	}
	ids := make([]string, len(rows))
	byStatus := map[string]int{}
	for i, r := range rows {
		ids[i] = r.ID
		byStatus[r.Status]++
	}
	var eventsDeleted int
	if err := tx.NewRaw(
		"SELECT COUNT(*) FROM scheduled_change_events WHERE change_id IN (?)",
		bun.In(ids),
	).Scan(ctx, &eventsDeleted); err != nil {
		return PurgeBatchResult{}, err
	}
	if !dryRun {
		if _, err := tx.NewDelete().Model((*ScheduledChangeRow)(nil)).
			Where("id IN (?)", bun.In(ids)).Exec(ctx); err != nil {
			return PurgeBatchResult{}, err
		}
	}
	return PurgeBatchResult{DeletedIDs: ids, ByStatus: byStatus, EventsDeleted: eventsDeleted}, nil
}

// ReclaimSpace reclaims disk space after deletes. Must not run inside a store transaction.
func (s *Store) ReclaimSpace(ctx context.Context, mode string) error {
	if mode == "off" || mode == "" {
		return nil
	}
	if _, ok := ctx.Value(txKey{}).(bun.Tx); ok {
		return errors.New("store: reclaim_space cannot run inside a store transaction")
	}
	if s.backend == "sqlite" {
		return s.reclaimSQLite(ctx, mode)
	}
	return s.reclaimPostgres(ctx, mode)
}

func (s *Store) reclaimSQLite(ctx context.Context, mode string) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	sqldb := db.DB
	if mode == "full" {
		if _, err := sqldb.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return err
		}
		if _, err := sqldb.ExecContext(ctx, "VACUUM"); err != nil {
			return err
		}
		s.sqliteIncrementalReady = true
		return nil
	}
	var current int
	if err := sqldb.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&current); err != nil {
		return err
	}
	if current != 2 && !s.sqliteIncrementalReady {
		if _, err := sqldb.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
			return err
		}
		if _, err := sqldb.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return err
		}
		if _, err := sqldb.ExecContext(ctx, "VACUUM"); err != nil {
			return err
		}
		s.sqliteIncrementalReady = true
		return nil
	}
	s.sqliteIncrementalReady = true
	if _, err := sqldb.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	_, err = sqldb.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return err
}

func (s *Store) reclaimPostgres(ctx context.Context, mode string) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	tables := []string{
		"scheduled_changes", "scheduled_operations", "scheduled_prerequisites",
		"scheduled_change_events", "webhook_outbox",
	}
	verb := "VACUUM"
	if mode == "full" {
		verb = "VACUUM FULL"
	}
	sqldb := db.DB
	for _, table := range tables {
		if _, err := sqldb.ExecContext(ctx, verb+" "+table); err != nil {
			return err
		}
	}
	return nil
}

// GetEvents returns audit events for a change.
func (s *Store) GetEvents(ctx context.Context, changeID string) ([]ScheduledChangeEvent, error) {
	var out []ScheduledChangeEvent
	err := s.runTx(ctx, "get_events", func(ctx context.Context, tx bun.Tx) error {
		var err error
		out, err = s.getEventsTx(ctx, tx, changeID)
		return err
	})
	return out, err
}

func (s *Store) getEventsTx(ctx context.Context, tx bun.IDB, changeID string) ([]ScheduledChangeEvent, error) {
	var rows []ScheduledChangeEventRow
	if err := tx.NewSelect().Model(&rows).
		Where("change_id = ?", changeID).
		Order("id ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]ScheduledChangeEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, eventFromRow(&r))
	}
	return out, nil
}

// ListEvents lists audit events across changes with filters and pagination.
func (s *Store) ListEvents(ctx context.Context, opts ListEventsOpts) ([]AuditEvent, int, error) {
	var events []AuditEvent
	var total int
	err := s.runTx(ctx, "list_events", func(ctx context.Context, tx bun.Tx) error {
		where := []string{"1=1"}
		args := []any{}
		if len(opts.Events) > 0 {
			where = append(where, "e.event IN (?)")
			args = append(args, bun.In(opts.Events))
		}
		if opts.Actor != "" {
			where = append(where, "e.actor LIKE ?")
			args = append(args, "%"+opts.Actor+"%")
		}
		zone := opts.Zone
		if zone != "" {
			if !strings.HasSuffix(zone, ".") {
				zone += "."
			}
			where = append(where, "c.zone = ?")
			args = append(args, zone)
		}
		if opts.ChangeID != "" {
			where = append(where, "e.change_id = ?")
			args = append(args, opts.ChangeID)
		}
		if opts.Since != nil {
			where = append(where, "e.ts >= ?")
			args = append(args, FormatISO(*opts.Since))
		}
		if opts.Until != nil {
			where = append(where, "e.ts <= ?")
			args = append(args, FormatISO(*opts.Until))
		}
		if opts.Q != "" {
			like := "%" + opts.Q + "%"
			where = append(where, `(
				e.event LIKE ? OR COALESCE(e.actor, '') LIKE ? OR COALESCE(CAST(e.detail AS TEXT), '') LIKE ?
				OR c.name LIKE ? OR c.zone LIKE ?
			)`)
			args = append(args, like, like, like, like, like)
		}
		clause := strings.Join(where, " AND ")
		countArgs := append([]any{}, args...)
		if err := tx.NewRaw(
			`SELECT COUNT(*) FROM scheduled_change_events e
			 JOIN scheduled_changes c ON c.id = e.change_id WHERE `+clause,
			countArgs...,
		).Scan(ctx, &total); err != nil {
			return err
		}
		limit := opts.Limit
		if limit < 1 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}
		offset := opts.Offset
		if offset < 0 {
			offset = 0
		}
		type row struct {
			ID           int64    `bun:"id"`
			TS           UtcTime  `bun:"ts"`
			Event        string   `bun:"event"`
			Actor        *string  `bun:"actor"`
			Detail       JSONText `bun:"detail"`
			ChangeID     string   `bun:"change_id"`
			ChangeName   string   `bun:"change_name"`
			Zone         string   `bun:"zone"`
			ChangeStatus string   `bun:"change_status"`
		}
		queryArgs := append(append([]any{}, args...), limit, offset)
		var rows []row
		if err := tx.NewRaw(
			`SELECT e.id, e.ts, e.event, e.actor, e.detail, e.change_id,
			        c.name AS change_name, c.zone, c.status AS change_status
			 FROM scheduled_change_events e
			 JOIN scheduled_changes c ON c.id = e.change_id
			 WHERE `+clause+`
			 ORDER BY e.ts DESC, e.id DESC
			 LIMIT ? OFFSET ?`,
			queryArgs...,
		).Scan(ctx, &rows); err != nil {
			return err
		}
		events = make([]AuditEvent, 0, len(rows))
		for _, r := range rows {
			detail, _ := UnmarshalJSON[map[string]any](r.Detail)
			events = append(events, AuditEvent{
				ID: r.ID, TS: r.TS.Std(), Event: r.Event, Actor: r.Actor, Detail: detail,
				ChangeID: r.ChangeID, ChangeName: r.ChangeName, Zone: r.Zone, ChangeStatus: r.ChangeStatus,
			})
		}
		return nil
	})
	return events, total, err
}

// ListEventsOpts filters ListEvents.
type ListEventsOpts struct {
	Events   []string
	Actor    string
	Zone     string
	ChangeID string
	Q        string
	Since    *time.Time
	Until    *time.Time
	Limit    int
	Offset   int
}

func (s *Store) rowToChange(ctx context.Context, tx bun.IDB, row *ScheduledChangeRow, includeEvents bool) (*ScheduledChange, error) {
	ops, err := s.loadOperations(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	prereqs, err := s.loadPrerequisites(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	var events []ScheduledChangeEvent
	if includeEvents {
		events, err = s.getEventsTx(ctx, tx, row.ID)
		if err != nil {
			return nil, err
		}
	}
	source := row.Source
	if source == "" {
		source = "scheduler"
	}
	kind := row.Kind
	if kind == "" {
		kind = KindRecords
	}
	ch := &ScheduledChange{
		ID: row.ID, Name: row.Name, Description: row.Description, Zone: row.Zone,
		Status: row.Status, Source: source, Kind: kind, Payload: json.RawMessage(row.Payload),
		AutoPrerequisites: row.AutoPrerequisites,
		CreatedAt:         row.CreatedAt.Std(), CreatedBy: row.CreatedBy,
		UpdatedAt: row.UpdatedAt.Std(), Attempts: row.Attempts,
		LastError: row.LastError, ResultRcode: row.ResultRcode, NewSerial: row.NewSerial,
		Operations: ops, Prerequisites: prereqs, Events: events,
	}
	ch.ScheduledAt = utcPtr(row.ScheduledAt)
	ch.NotValidAfter = utcPtr(row.NotValidAfter)
	ch.NextAttemptAt = utcPtr(row.NextAttemptAt)
	ch.AppliedAt = utcPtr(row.AppliedAt)
	ch.RevertedAt = utcPtr(row.RevertedAt)
	return ch, nil
}

func utcPtr(t *UtcTime) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	tt := t.Std()
	return &tt
}

func (s *Store) loadOperations(ctx context.Context, tx bun.IDB, changeID string) ([]ScheduledOperation, error) {
	var rows []ScheduledOperationRow
	if err := tx.NewSelect().Model(&rows).
		Where("change_id = ?", changeID).
		Order("seq ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]ScheduledOperation, 0, len(rows))
	for _, r := range rows {
		records, _ := UnmarshalJSON[[]string](r.Records)
		prior, _ := UnmarshalJSON[[]string](r.PriorRecords)
		op := ScheduledOperation{
			Action: r.Action, Name: r.Name, Type: r.Type, RDClass: r.RDClass, TTL: r.TTL,
			Records: records, PriorTTL: r.PriorTTL, PriorRecords: prior,
		}
		op.SnapshotAt = utcPtr(r.SnapshotAt)
		out = append(out, op)
	}
	return out, nil
}

func (s *Store) loadPrerequisites(ctx context.Context, tx bun.IDB, changeID string) ([]ChangePrerequisite, error) {
	var rows []ScheduledPrerequisiteRow
	if err := tx.NewSelect().Model(&rows).
		Where("change_id = ?", changeID).
		Order("seq ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]ChangePrerequisite, 0, len(rows))
	for _, r := range rows {
		out = append(out, ChangePrerequisite{
			PrereqType: r.PrereqType, Name: r.Name, RDType: r.RDType, RDClass: r.RDClass, Data: r.Data,
		})
	}
	return out, nil
}

func eventFromRow(r *ScheduledChangeEventRow) ScheduledChangeEvent {
	detail, _ := UnmarshalJSON[map[string]any](r.Detail)
	return ScheduledChangeEvent{
		ID: r.ID, ChangeID: r.ChangeID, TS: r.TS.Std(),
		Event: r.Event, Actor: r.Actor, Detail: detail,
	}
}

// --- Webhook outbox -------------------------------------------------------

// OutboxEnqueue inserts a webhook delivery job.
func (s *Store) OutboxEnqueue(ctx context.Context, id string, eventJSON []byte, target string) error {
	return s.runTx(ctx, "outbox_enqueue", func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		nowT := NewUtcTime(now)
		row := &WebhookOutboxRow{
			ID:            id,
			EventJSON:     JSONText(eventJSON),
			Target:        target,
			Attempts:      0,
			NextAttemptAt: &nowT,
			CreatedAt:     nowT,
		}
		_, err := tx.NewInsert().Model(row).Exec(ctx)
		return err
	})
}

// OutboxClaimBatch claims up to limit undelivered outbox rows that are due.
func (s *Store) OutboxClaimBatch(ctx context.Context, limit int, now time.Time) ([]WebhookOutboxItem, error) {
	if limit <= 0 {
		limit = 10
	}
	var out []WebhookOutboxItem
	err := s.runTx(ctx, "outbox_claim_batch", func(ctx context.Context, tx bun.Tx) error {
		nowISO := FormatISO(now.UTC())
		var q string
		if s.isPostgres() {
			q = `
			SELECT id FROM webhook_outbox
			WHERE delivered_at IS NULL
			  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY next_attempt_at ASC NULLS LAST, created_at ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED`
		} else {
			// SQLite has no NULLS LAST; NULLs sort first by default in ASC.
			q = `
			SELECT id FROM webhook_outbox
			WHERE delivered_at IS NULL
			  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT ?`
		}
		var ids []string
		if err := tx.NewRaw(q, nowISO, limit).Scan(ctx, &ids); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if _, err := tx.NewUpdate().Model((*WebhookOutboxRow)(nil)).
			Where("id IN (?)", bun.In(ids)).
			Set("attempts = attempts + 1").
			Exec(ctx); err != nil {
			return err
		}
		var rows []WebhookOutboxRow
		if err := tx.NewSelect().Model(&rows).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
			return err
		}
		out = make([]WebhookOutboxItem, 0, len(rows))
		for _, r := range rows {
			out = append(out, outboxFromRow(&r))
		}
		return nil
	})
	return out, err
}

// OutboxMarkDelivered marks an outbox row as delivered.
func (s *Store) OutboxMarkDelivered(ctx context.Context, id string) error {
	return s.runTx(ctx, "outbox_mark_delivered", func(ctx context.Context, tx bun.Tx) error {
		now := FormatISO(time.Now().UTC())
		_, err := tx.NewUpdate().Model((*WebhookOutboxRow)(nil)).
			Where("id = ?", id).
			Set("delivered_at = ?", now).
			Set("last_error = NULL").
			Exec(ctx)
		return err
	})
}

// OutboxMarkRetry schedules a retry for a failed delivery.
func (s *Store) OutboxMarkRetry(ctx context.Context, id string, lastError string, nextAttemptAt time.Time) error {
	return s.runTx(ctx, "outbox_mark_retry", func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model((*WebhookOutboxRow)(nil)).
			Where("id = ?", id).
			Set("last_error = ?", lastError).
			Set("next_attempt_at = ?", FormatISO(nextAttemptAt.UTC())).
			Exec(ctx)
		return err
	})
}

// OutboxPurgeDelivered deletes delivered outbox rows older than cutoff.
func (s *Store) OutboxPurgeDelivered(ctx context.Context, olderThan time.Time) (int64, error) {
	var n int64
	err := s.runTx(ctx, "outbox_purge_delivered", func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*WebhookOutboxRow)(nil)).
			Where("delivered_at IS NOT NULL AND delivered_at < ?", FormatISO(olderThan.UTC())).
			Exec(ctx)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

func outboxFromRow(r *WebhookOutboxRow) WebhookOutboxItem {
	return WebhookOutboxItem{
		ID: r.ID, EventJSON: json.RawMessage(r.EventJSON), Target: r.Target,
		Attempts: r.Attempts, NextAttemptAt: utcPtr(r.NextAttemptAt),
		CreatedAt: r.CreatedAt.Std(), DeliveredAt: utcPtr(r.DeliveredAt), LastError: r.LastError,
	}
}
