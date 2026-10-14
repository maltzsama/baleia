package queryengine

import (
	"context"
	"errors"
	"fmt"
)

// ErrorCode is the bounded set of failures an engine reports
// (docs/adr/0008). The API maps codes to responses; native messages never
// cross the API boundary.
type ErrorCode string

const (
	CodeSyntax      ErrorCode = "syntax"
	CodeNotFound    ErrorCode = "not_found"
	CodePermission  ErrorCode = "permission_denied"
	CodeTimeout     ErrorCode = "timeout"
	CodeCancelled   ErrorCode = "cancelled"
	CodeResource    ErrorCode = "resource_exhausted"
	CodeUnavailable ErrorCode = "engine_unavailable"
	CodeReadOnly    ErrorCode = "read_only_unsupported"
	CodeConflict    ErrorCode = "conflict" // serialization/deadlock — never retried automatically
	CodeInternal    ErrorCode = "internal"
)

// Error is a typed engine failure. Message is safe to log; Native is the
// engine's own code (SQLSTATE, Trino errorName, …).
type Error struct {
	Code    ErrorCode
	Message string
	Engine  string
	Native  string
}

func (e *Error) Error() string {
	if e.Native != "" {
		return fmt.Sprintf("%s: %s (%s %s)", e.Code, e.Message, e.Engine, e.Native)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is matches another *Error by code, so errors.Is(err, &Error{Code: c}) works.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// NewError builds an *Error.
func NewError(code ErrorCode, engine, native, msg string) *Error {
	return &Error{Code: code, Message: msg, Engine: engine, Native: native}
}

// CodeOf returns the code of an engine error, CodeCancelled for a cancelled
// context, CodeTimeout for an expired one, and CodeInternal for anything else.
func CodeOf(err error) ErrorCode {
	var e *Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &e):
		return e.Code
	case errors.Is(err, context.Canceled):
		return CodeCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	}
	return CodeInternal
}

// ErrCancelUnsupported is returned by Cancel on engines whose CancelScope is
// in_process.
var ErrCancelUnsupported = errors.New("cancel by handle is not supported by this engine")

// ErrKindNotRegistered means no adapter registered the engine's kind.
var ErrKindNotRegistered = errors.New("engine kind not registered")
