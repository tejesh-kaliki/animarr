package db

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Bool is a SQLite INTEGER 0/1 column.
type Bool bool

// Value stores 1 or 0.
func (b Bool) Value() (driver.Value, error) {
	if b {
		return int64(1), nil
	}
	return int64(0), nil
}

// Scan reads an INTEGER flag.
func (b *Bool) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		*b = v != 0
	case int:
		*b = v != 0
	case bool:
		*b = Bool(v)
	case []byte:
		*b = string(v) == "1"
	case string:
		*b = v == "1"
	case nil:
		*b = false
	default:
		return fmt.Errorf("scan bool from %T", src)
	}
	return nil
}

// UnixTime is a NOT NULL unix-seconds INTEGER column.
type UnixTime time.Time

// Time returns the UTC instant.
func (t UnixTime) Time() time.Time {
	return time.Time(t).UTC()
}

// Value stores unix seconds. A zero time is stored as 0.
func (t UnixTime) Value() (driver.Value, error) {
	tt := time.Time(t)
	if tt.IsZero() {
		return int64(0), nil
	}
	return tt.UTC().Unix(), nil
}

// Scan reads unix seconds.
func (t *UnixTime) Scan(src any) error {
	if src == nil {
		*t = UnixTime{}
		return nil
	}
	switch v := src.(type) {
	case int64:
		*t = UnixTime(time.Unix(v, 0).UTC())
	case int:
		*t = UnixTime(time.Unix(int64(v), 0).UTC())
	case time.Time:
		*t = UnixTime(v.UTC())
	default:
		return fmt.Errorf("scan unix time from %T", src)
	}
	return nil
}

// NullUnixTime is a nullable unix-seconds INTEGER column.
type NullUnixTime struct {
	Time  time.Time
	Valid bool
}

// Ptr is nil when the column is NULL or zero.
func (n NullUnixTime) Ptr() *time.Time {
	if !n.Valid || n.Time.IsZero() {
		return nil
	}
	t := n.Time.UTC()
	return &t
}

// Value stores NULL or unix seconds.
func (n NullUnixTime) Value() (driver.Value, error) {
	if !n.Valid || n.Time.IsZero() {
		return nil, nil
	}
	return n.Time.UTC().Unix(), nil
}

// Scan reads NULL or unix seconds.
func (n *NullUnixTime) Scan(src any) error {
	if src == nil {
		*n = NullUnixTime{}
		return nil
	}
	switch v := src.(type) {
	case int64:
		n.Time = time.Unix(v, 0).UTC()
		n.Valid = true
	case int:
		n.Time = time.Unix(int64(v), 0).UTC()
		n.Valid = true
	case time.Time:
		n.Time = v.UTC()
		n.Valid = true
	default:
		return fmt.Errorf("scan null unix time from %T", src)
	}
	return nil
}

// NewUnixTime converts a time. A zero time becomes the zero UnixTime.
func NewUnixTime(t time.Time) UnixTime {
	if t.IsZero() {
		return UnixTime{}
	}
	return UnixTime(t.UTC())
}

// NewNullUnixTime converts a pointer. Nil or zero is NULL.
func NewNullUnixTime(t *time.Time) NullUnixTime {
	if t == nil || t.IsZero() {
		return NullUnixTime{}
	}
	return NullUnixTime{Time: t.UTC(), Valid: true}
}
