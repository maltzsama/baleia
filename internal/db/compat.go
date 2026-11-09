package db

import (
	"errors"
	"reflect"

	"gorm.io/gorm"
)

// ErrNoRows is the store's "no row matched" sentinel. It is GORM's
// ErrRecordNotFound, so a repository that reads through GORM and a caller that
// classifies the result share one value without either importing a driver.
var ErrNoRows = gorm.ErrRecordNotFound

// PGError is the portable form of the PostgreSQL error the store and its
// callers inspect (SQLSTATE plus the constraint the server named). The GORM
// store builds it from a translated error; no driver type leaks through the
// store boundary.
type PGError struct {
	// Code is the SQLSTATE, such as 23505 for unique_violation.
	Code string
	// ConstraintName is the index or constraint the server named, when known.
	ConstraintName string
	// Message is the server's message, or a stable substitute.
	Message string
	// TableName is the table the server named, when known.
	TableName string
}

// Error implements error.
func (e *PGError) Error() string {
	if e.ConstraintName != "" {
		return e.Message + " (constraint " + e.ConstraintName + ")"
	}
	return e.Message
}

// SQLState returns the SQLSTATE, matching the PostgreSQL driver's interface.
func (e *PGError) SQLState() string { return e.Code }

// The SQLSTATE values mapped from GORM's translated sentinels.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

// sqlStateError is the method every PostgreSQL driver error exposes; naming it
// here keeps driver packages out of the callers.
type sqlStateError interface {
	SQLState() string
}

// AsPGError reports whether err is, or wraps, a *PGError. A raw driver error
// (still wrapped by GORM's translated error) is adapted on the way out, so the
// store and the services classify PostgreSQL failures without importing a
// driver. The constraint and table names are struct fields, so they are read
// reflectively from the driver error when present.
func AsPGError(err error) (*PGError, bool) {
	if err == nil {
		return nil, false
	}
	var pg *PGError
	if errors.As(err, &pg) {
		return pg, true
	}
	var state sqlStateError
	if errors.As(err, &state) {
		out := &PGError{Code: state.SQLState(), Message: err.Error()}
		readStringField(out, state)
		return out, true
	}
	// GORM's translated sentinels cover a driver that does not expose
	// SQLSTATE (the MySQL backend).
	switch {
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return &PGError{Code: sqlStateUniqueViolation, Message: "duplicate key value violates unique constraint"}, true
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		return &PGError{Code: sqlStateForeignKeyViolation, Message: "foreign key violation"}, true
	}
	return nil, false
}

// readStringField copies the driver error's constraint and table names onto
// pg when the value is a struct with those string fields.
func readStringField(pg *PGError, state sqlStateError) {
	v := reflect.ValueOf(state)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	field := func(name string) string {
		f := v.FieldByName(name)
		if !f.IsValid() || f.Kind() != reflect.String {
			return ""
		}
		return f.String()
	}
	pg.ConstraintName = field("ConstraintName")
	pg.TableName = field("TableName")
}
