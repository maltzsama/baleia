// Package repo is the GORM-backed persistence for Baleia's store (ADR 0020).
// Each type implements the narrow Store interface of one domain service while
// the rest of the store still runs through sqlc (#769–#774).
package repo

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// UUIDString renders a sqlc pgtype.UUID as text.
func UUIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// PGUUID parses a text UUID into the sqlc type.
func PGUUID(s string) pgtype.UUID {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

// PGTime wraps a time as a valid timestamptz.
func PGTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// PGTimePtr wraps an optional time.
func PGTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// TimePtr unwraps an optional timestamptz.
func TimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// NewUUID returns a fresh UUIDv4 as text for backends without a UUID default.
func NewUUID() string { return uuid.NewString() }
