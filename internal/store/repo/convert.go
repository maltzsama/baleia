// Package repo is the GORM-backed persistence for Baleia's store (ADR 0020).
// Each type implements the narrow Store interface of one domain service
// (#769–#774).
package repo

import (
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// uniqueViolationCode is PostgreSQL's SQLSTATE for a unique-index refusal;
// MySQL and MariaDB report their own code, but callers only inspect this one
// through the translated error GORM hands back.
const uniqueViolationCode = "23505"

// asUniqueViolation turns GORM's translated duplicate-key error back into the
// *db.PGError callers inspect. constraint names the unique index when the
// caller knows it, so the specific 409 message survives (ADR 0020 keeps the
// store API GORM-shaped while callers keep their sqlc-era error checks).
func asUniqueViolation(err error, constraint string) error {
	if err == nil || !errors.Is(err, gorm.ErrDuplicatedKey) {
		return err
	}
	return &db.PGError{
		Code:           uniqueViolationCode,
		ConstraintName: constraint,
		Message:        "duplicate key value violates unique constraint \"" + constraint + "\"",
	}
}

// UUIDString renders a db.UUID as text.
func UUIDString(u db.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// uuidText renders a UUID for a query, using the zero UUID when unset so a
// comparison stays well-typed (the previous query layer passed the zero value
// the same way).
func uuidText(u db.UUID) string {
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

// PGUUID parses a text UUID into the db.UUID shape.
func PGUUID(s string) db.UUID {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return db.UUID{}
	}
	return db.UUID{Bytes: parsed, Valid: true}
}

// PGTime wraps a time as a valid timestamptz.
func PGTime(t time.Time) db.Timestamptz {
	return db.Timestamptz{Time: t, Valid: true}
}

// PGTimePtr wraps an optional time.
func PGTimePtr(t *time.Time) db.Timestamptz {
	if t == nil {
		return db.Timestamptz{}
	}
	return db.Timestamptz{Time: *t, Valid: true}
}

// TimePtr unwraps an optional timestamptz.
func TimePtr(t db.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// NewUUID returns a fresh UUIDv4 as text for backends without a UUID default.
func NewUUID() string { return uuid.NewString() }
