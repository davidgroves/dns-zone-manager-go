package store

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// Change statuses matching the Python scheduler.
const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusRunning   = "running"
	StatusApplied   = "applied"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
	StatusReverted  = "reverted"
)

var (
	EditableStatuses = map[string]struct{}{
		StatusDraft: {}, StatusScheduled: {}, StatusFailed: {},
	}
	PendingStatuses = map[string]struct{}{
		StatusDraft: {}, StatusScheduled: {}, StatusFailed: {}, StatusRunning: {},
	}
	ConflictStatuses = map[string]struct{}{
		StatusDraft: {}, StatusScheduled: {}, StatusFailed: {},
	}
	CancellableStatuses = map[string]struct{}{
		StatusDraft: {}, StatusScheduled: {}, StatusFailed: {},
	}
)

// JSONText stores JSON as TEXT (SQLite) or JSONB (Postgres) via driver.Valuer/Scanner.
type JSONText []byte

func (j JSONText) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return []byte(j), nil
}

func (j *JSONText) Scan(src any) error {
	if src == nil {
		*j = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		cp := make([]byte, len(v))
		copy(cp, v)
		*j = cp
		return nil
	case string:
		*j = []byte(v)
		return nil
	default:
		return fmt.Errorf("store.JSONText: unsupported Scan type %T", src)
	}
}

func MarshalJSON(v any) (JSONText, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return JSONText(b), nil
}

func UnmarshalJSON[T any](j JSONText) (T, error) {
	var out T
	if len(j) == 0 {
		return out, nil
	}
	err := json.Unmarshal(j, &out)
	return out, err
}

// ScheduledChangeRow is the bun model for scheduled_changes.
type ScheduledChangeRow struct {
	bun.BaseModel `bun:"table:scheduled_changes"`

	ID                string   `bun:"id,pk"`
	Name              string   `bun:"name,notnull"`
	Description       *string  `bun:"description"`
	Zone              string   `bun:"zone,notnull"`
	Status            string   `bun:"status,notnull"`
	ScheduledAt       *UtcTime `bun:"scheduled_at"`
	NotValidAfter     *UtcTime `bun:"not_valid_after"`
	AutoPrerequisites bool     `bun:"auto_prerequisites,notnull"`
	CreatedAt         UtcTime  `bun:"created_at,notnull"`
	CreatedBy         *string  `bun:"created_by"`
	UpdatedAt         UtcTime  `bun:"updated_at,notnull"`
	Attempts          int      `bun:"attempts,notnull"`
	NextAttemptAt     *UtcTime `bun:"next_attempt_at"`
	LastError         *string  `bun:"last_error"`
	AppliedAt         *UtcTime `bun:"applied_at"`
	ResultRcode       *string  `bun:"result_rcode"`
	NewSerial         *int64   `bun:"new_serial"`
	RevertedAt        *UtcTime `bun:"reverted_at"`
	LeaseOwner        *string  `bun:"lease_owner"`
	LeaseExpiresAt    *UtcTime `bun:"lease_expires_at"`
	Source            string   `bun:"source,notnull"`
}

// ScheduledOperationRow is the bun model for scheduled_operations.
type ScheduledOperationRow struct {
	bun.BaseModel `bun:"table:scheduled_operations"`

	ChangeID     string   `bun:"change_id,pk"`
	Seq          int      `bun:"seq,pk"`
	Action       string   `bun:"action,notnull"`
	Name         string   `bun:"name,notnull"`
	Type         string   `bun:"type,notnull"`
	RDClass      string   `bun:"rdclass,notnull"`
	TTL          uint32   `bun:"ttl,notnull"`
	Records      JSONText `bun:"records"`
	PriorTTL     *uint32  `bun:"prior_ttl"`
	PriorRecords JSONText `bun:"prior_records"`
	SnapshotAt   *UtcTime `bun:"snapshot_at"`
}

// ScheduledPrerequisiteRow is the bun model for scheduled_prerequisites.
type ScheduledPrerequisiteRow struct {
	bun.BaseModel `bun:"table:scheduled_prerequisites"`

	ChangeID   string  `bun:"change_id,pk"`
	Seq        int     `bun:"seq,pk"`
	PrereqType string  `bun:"prereq_type,notnull"`
	Name       string  `bun:"name,notnull"`
	RDType     *string `bun:"rdtype"`
	RDClass    string  `bun:"rdclass,notnull"`
	Data       *string `bun:"data"`
}

// ScheduledChangeEventRow is the bun model for scheduled_change_events.
type ScheduledChangeEventRow struct {
	bun.BaseModel `bun:"table:scheduled_change_events"`

	ID       int64    `bun:"id,pk,autoincrement"`
	ChangeID string   `bun:"change_id,notnull"`
	TS       UtcTime  `bun:"ts,notnull"`
	Event    string   `bun:"event,notnull"`
	Actor    *string  `bun:"actor"`
	Detail   JSONText `bun:"detail"`
}

// WebhookOutboxRow is the bun model for webhook_outbox.
type WebhookOutboxRow struct {
	bun.BaseModel `bun:"table:webhook_outbox"`

	ID            string   `bun:"id,pk"`
	EventJSON     JSONText `bun:"event_json,notnull"`
	Target        string   `bun:"target,notnull"`
	Attempts      int      `bun:"attempts,notnull"`
	NextAttemptAt *UtcTime `bun:"next_attempt_at"`
	CreatedAt     UtcTime  `bun:"created_at,notnull"`
	DeliveredAt   *UtcTime `bun:"delivered_at"`
	LastError     *string  `bun:"last_error"`
}

// AtomicOperation is a DNS update operation to store or conflict-check.
type AtomicOperation struct {
	Action  string   `json:"action"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	RDClass string   `json:"rdclass"`
	TTL     uint32   `json:"ttl"`
	Records []string `json:"records"`
}

func (op AtomicOperation) Normalized() AtomicOperation {
	out := op
	if out.RDClass == "" {
		out.RDClass = "IN"
	}
	return out
}

// ChangePrerequisite is a DNS UPDATE prerequisite attached to a change.
type ChangePrerequisite struct {
	PrereqType string  `json:"prereq_type"`
	Name       string  `json:"name"`
	RDType     *string `json:"rdtype"`
	RDClass    string  `json:"rdclass"`
	Data       *string `json:"data"`
}

// ChangeCreateData is the internal create payload.
type ChangeCreateData struct {
	Name              string
	Zone              string
	Operations        []AtomicOperation
	Prerequisites     []ChangePrerequisite
	Description       *string
	ScheduledAt       *time.Time
	NotValidAfter     *time.Time
	AutoPrerequisites bool
	CreatedBy         *string
}

// ChangeUpdateData is the internal update payload.
type ChangeUpdateData struct {
	Name               *string
	Description        *string
	ClearDescription   bool
	Operations         []AtomicOperation
	Prerequisites      []ChangePrerequisite
	HasPrerequisites   bool
	ScheduledAt        *time.Time
	ClearScheduledAt   bool
	NotValidAfter      *time.Time
	ClearNotValidAfter bool
	AutoPrerequisites  *bool
	Actor              *string
}

// OpSnapshot is pre-apply RRset state for one operation.
type OpSnapshot struct {
	Seq          int
	PriorTTL     *uint32
	PriorRecords []string
	SnapshotAt   time.Time
}

// PurgeBatchResult is the outcome of one retention delete (or dry-run) batch.
type PurgeBatchResult struct {
	DeletedIDs    []string
	ByStatus      map[string]int
	EventsDeleted int
}

func (r PurgeBatchResult) ChangesDeleted() int { return len(r.DeletedIDs) }

// ScheduledOperation is a stored operation including optional revert snapshot.
type ScheduledOperation struct {
	Action       string
	Name         string
	Type         string
	RDClass      string
	TTL          uint32
	Records      []string
	PriorTTL     *uint32
	PriorRecords []string
	SnapshotAt   *time.Time
}

// ScheduledChangeEvent is an audit-trail event for a scheduled change.
type ScheduledChangeEvent struct {
	ID       int64
	ChangeID string
	TS       time.Time
	Event    string
	Actor    *string
	Detail   map[string]any
}

// AuditEvent is a scheduled-change audit event with joined change metadata.
type AuditEvent struct {
	ID           int64
	TS           time.Time
	Event        string
	Actor        *string
	Detail       map[string]any
	ChangeID     string
	ChangeName   string
	Zone         string
	ChangeStatus string
}

// ScheduledChange is the full change including operations and prerequisites.
type ScheduledChange struct {
	ID                string
	Name              string
	Description       *string
	Zone              string
	Status            string
	Source            string
	ScheduledAt       *time.Time
	NotValidAfter     *time.Time
	AutoPrerequisites bool
	CreatedAt         time.Time
	CreatedBy         *string
	UpdatedAt         time.Time
	Attempts          int
	NextAttemptAt     *time.Time
	LastError         *string
	AppliedAt         *time.Time
	ResultRcode       *string
	NewSerial         *int64
	RevertedAt        *time.Time
	Operations        []ScheduledOperation
	Prerequisites     []ChangePrerequisite
	Events            []ScheduledChangeEvent
}

// Conflict is another change that touches the same name/type/class.
type Conflict struct {
	OtherID   string
	OtherName string
	Name      string
	Type      string
	RDClass   string
}

// WebhookOutboxItem is a claimed/persisted outbox row.
type WebhookOutboxItem struct {
	ID            string
	EventJSON     json.RawMessage
	Target        string
	Attempts      int
	NextAttemptAt *time.Time
	CreatedAt     time.Time
	DeliveredAt   *time.Time
	LastError     *string
}
