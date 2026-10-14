package connections

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ErrValidation wraps every validation failure.
var ErrValidation = errors.New("validation_error")

// FieldError is a validation failure of one field.
type FieldError struct {
	Key     string
	Message string
}

// ValidationError lists the invalid fields of a config.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Key+": "+f.Message)
	}
	return "invalid connection config: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

// ValidateSchema checks a config against a schema and returns a normalized copy:
// values trimmed, defaults applied, unknown keys rejected, secrets kept apart
// from values. Providers call it first and add their own checks.
func ValidateSchema(schema []Field, cfg Config) (Config, error) {
	out := Config{
		ConnectionID: cfg.ConnectionID,
		Kind:         cfg.Kind,
		Values:       map[string]string{},
		Secrets:      map[string]string{},
	}
	var errs []FieldError
	known := make(map[string]Field, len(schema))
	for _, f := range schema {
		known[f.Key] = f
	}

	for _, src := range []struct {
		m      map[string]string
		secret bool
	}{{cfg.Values, false}, {cfg.Secrets, true}} {
		for _, k := range sortedKeys(src.m) {
			f, ok := known[k]
			switch {
			case !ok:
				errs = append(errs, FieldError{k, "unknown field"})
			case f.Secret() != src.secret && f.Secret():
				errs = append(errs, FieldError{k, "secret field sent as a value"})
			case f.Secret() != src.secret:
				errs = append(errs, FieldError{k, "value field sent as a secret"})
			}
		}
	}

	for _, f := range schema {
		var v string
		if f.Secret() {
			v = cfg.Secrets[f.Key] // secrets are not trimmed: spaces may be part of them
		} else {
			v = strings.TrimSpace(cfg.Values[f.Key])
		}
		if v == "" {
			v = f.Default
		}
		if v == "" {
			if f.Required {
				errs = append(errs, FieldError{f.Key, "required"})
			}
			continue
		}
		if msg := checkType(f, v); msg != "" {
			errs = append(errs, FieldError{f.Key, msg})
			continue
		}
		if f.Secret() {
			out.Secrets[f.Key] = v
		} else {
			out.Values[f.Key] = v
		}
	}

	if len(errs) > 0 {
		return Config{}, &ValidationError{Fields: errs}
	}
	return out, nil
}

func checkType(f Field, v string) string {
	switch f.Type {
	case FieldInt:
		if _, err := strconv.Atoi(v); err != nil {
			return "must be an integer"
		}
	case FieldBool:
		if _, err := strconv.ParseBool(v); err != nil {
			return "must be true or false"
		}
	case FieldSelect:
		if !slices.Contains(f.Options, v) {
			return "must be one of " + strings.Join(f.Options, ", ")
		}
	case FieldURL:
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "must be an http or https URL"
		}
		if u.User != nil {
			return "must not contain credentials"
		}
	}
	return ""
}

// Port validates a TCP port value.
func Port(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("must be a port between 1 and 65535")
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
