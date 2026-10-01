package store

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// UtcTime is a timezone-aware UTC timestamp.
// On SQLite it is stored as an ISO-8601 text string; on PostgreSQL as timestamptz.
type UtcTime struct {
	time.Time
}

func NewUtcTime(t time.Time) UtcTime {
	return UtcTime{Time: t.UTC()}
}

func UtcNow() UtcTime {
	return NewUtcTime(time.Now().UTC())
}

func (t UtcTime) IsZero() bool {
	return t.Time.IsZero()
}

func (t UtcTime) Std() time.Time {
	return t.Time
}

// FormatISO returns an ISO-8601 UTC string suitable for SQLite storage and JSON audit detail.
func FormatISO(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func FormatISOPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := FormatISO(*t)
	return &s
}

func ParseISO(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05.999999999", s, time.UTC)
}

func (t UtcTime) Value() (driver.Value, error) {
	if t.Time.IsZero() {
		return nil, nil
	}
	// Always bind as ISO-8601 text so SQLite stores sortable strings.
	// PostgreSQL accepts the text and casts it to timestamptz.
	return FormatISO(t.Time), nil
}

func (t *UtcTime) Scan(src any) error {
	if src == nil {
		t.Time = time.Time{}
		return nil
	}
	switch v := src.(type) {
	case time.Time:
		t.Time = v.UTC()
		return nil
	case string:
		parsed, err := ParseISO(v)
		if err != nil {
			return fmt.Errorf("store.UtcTime: parse %q: %w", v, err)
		}
		t.Time = parsed
		return nil
	case []byte:
		parsed, err := ParseISO(string(v))
		if err != nil {
			return fmt.Errorf("store.UtcTime: parse %q: %w", v, err)
		}
		t.Time = parsed
		return nil
	default:
		return fmt.Errorf("store.UtcTime: unsupported Scan type %T", src)
	}
}

var (
	_ driver.Valuer = UtcTime{}
	_ sql.Scanner   = (*UtcTime)(nil)
)
