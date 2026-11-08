// Package repo is the GORM-backed persistence for Baleia's store (ADR 0020).
// Each type implements the narrow Store interface of one domain service while
// the rest of the store still runs through sqlc (#769–#774).
package repo

import (
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// uniqueViolationCode is PostgreSQL's SQLSTATE for a unique-index refusal;
// MySQL and MariaDB report their own code, but callers only inspect this one
// through the translated error GORM hands back.
const uniqueViolationCode = "23505"

// asUniqueViolation turns GORM's translated duplicate-key error back into the
// *pgconn.PgError callers inspect. constraint names the unique index when the
// caller knows it, so the specific 409 message survives (ADR 0020 keeps the
// store API GORM-shaped while callers keep their sqlc-era error checks).
func asUniqueViolation(err error, constraint string) error {
	if err == nil || !errors.Is(err, gorm.ErrDuplicatedKey) {
		return err
	}
	return &pgconn.PgError{
		Code:           uniqueViolationCode,
		ConstraintName: constraint,
		Message:        "duplicate key value violates unique constraint \"" + constraint + "\"",
	}
}

// UUIDString renders a sqlc pgtype.UUID as text.
func UUIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// uuidText renders a UUID for a query, using the zero UUID when unset so a
// comparison stays well-typed (sqlc passed the zero value the same way).
func uuidText(u pgtype.UUID) string {
	if !u.Valid {
		return "00000000-0000-0000-0000-000000000000"
	}
	return uuid.UUID(u.Bytes).String()
}

// jsonOrDefault keeps a NOT NULL json column inside its contract.
func jsonOrDefault(b []byte) model.JSON {
	if len(b) == 0 {
		return model.JSON("{}")
	}
	return model.JSON(b)
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
