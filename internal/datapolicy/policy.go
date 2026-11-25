// Package datapolicy holds Baleia's row filters and column masks (ADR 0028):
// the policies an admin authors, the restricted expression language they are
// written in, and the checks a draft passes before it can be published. OPA
// decides them and Trino enforces them; Baleia never rewrites SQL.
package datapolicy

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Kind is what a policy does.
type Kind string

const (
	// KindRowFilter limits the rows a subject sees.
	KindRowFilter Kind = "row_filter"
	// KindColumnMask replaces a column's values for a subject.
	KindColumnMask Kind = "column_mask"
)

// WriteEffect is what Trino 484 lets a write do to a table once a policy
// applies to the writer (ADR 0028, "Writes on policied tables").
type WriteEffect string

const (
	// WriteChecked: allowed when the new row matches the row filter, else
	// denied (PostgreSQL's WITH CHECK).
	WriteChecked WriteEffect = "checked"
	// WriteVisibleRows: applies only to the rows the filter leaves visible.
	WriteVisibleRows WriteEffect = "visible_rows"
	// WriteRefused: Trino refuses the statement.
	WriteRefused WriteEffect = "refused"
)

// Writes is what each kind of write does under a policy, for the identities
// it applies to.
type Writes struct {
	Insert, Update, Delete, Merge WriteEffect
}

// Writes reports what Trino 484 lets the policy's subjects write: under a row
// filter, INSERT is checked against it, DELETE removes only visible rows, and
// UPDATE and MERGE are refused; under a column mask every write is refused.
// Several policies on one table combine to the most restrictive effect.
func (p Policy) Writes() Writes {
	if p.Kind == KindRowFilter {
		return Writes{Insert: WriteChecked, Update: WriteRefused, Delete: WriteVisibleRows, Merge: WriteRefused}
	}
	return Writes{Insert: WriteRefused, Update: WriteRefused, Delete: WriteRefused, Merge: WriteRefused}
}

// Mode is how a row filter combines with the others of its table
// (PostgreSQL's model): permissive ones are OR-ed, restrictive ones AND-ed.
type Mode string

const (
	ModePermissive  Mode = "permissive"
	ModeRestrictive Mode = "restrictive"
)

// State says whether a policy takes part in the next publication.
type State string

const (
	StateEnabled  State = "enabled"
	StateDisabled State = "disabled"
)

// MaskTemplate records which UI template filled a mask's expression. It is a
// hint for the editor only: the expression is what applies.
type MaskTemplate string

const (
	MaskTemplateNull     MaskTemplate = "null"
	MaskTemplateConstant MaskTemplate = "constant"
	MaskTemplatePartial  MaskTemplate = "partial"
	MaskTemplateSHA256   MaskTemplate = "sha256"
	MaskTemplateCustom   MaskTemplate = "custom"
)

// Validation is the outcome of the last check of a policy's expression.
type Validation string

const (
	// ValidationPending: saved, not checked yet.
	ValidationPending Validation = "pending"
	// ValidationValid: the language check and the engine's type check passed.
	ValidationValid Validation = "valid"
	// ValidationInvalid: a check failed; the policy cannot be published.
	ValidationInvalid Validation = "invalid"
)

// Subject is one reference a policy applies to, in the bundle's own shape
// (ADR 0028, format_version 1): exactly one of User, Team, Group,
// ServiceAccount or Everyone. Everyone may carry Except, a list of named
// references. Users, teams and service accounts are referenced by id, groups
// by their identity-provider name.
type Subject struct {
	User           string    `json:"user,omitempty"`
	Team           string    `json:"team,omitempty"`
	Group          string    `json:"group,omitempty"`
	ServiceAccount string    `json:"service_account,omitempty"`
	Everyone       bool      `json:"everyone,omitempty"`
	Except         []Subject `json:"except,omitempty"`
}

// Target is the table, and for a mask the column, a policy applies to.
// Schema, table and column names are stored lowercase, as Trino resolves them.
type Target struct {
	CatalogID uuid.UUID
	Schema    string
	Table     string
	Column    string
}

// Policy is one row filter or column mask as authored. Expression is the
// author's text; Compiled is its checked canonical form, set only while
// Validation is valid.
type Policy struct {
	ID          uuid.UUID
	Name        string
	Description string
	Kind        Kind
	Target      Target
	Mode        Mode
	Priority    int32
	Subjects    []Subject
	Expression  string
	Template    MaskTemplate
	State       State
	Compiled    string
	Validation  Validation
	// InvalidReason and InvalidDetail say why Validation is invalid.
	InvalidReason string
	InvalidDetail string
	// Version counts the policy's saved changes; an update names the version
	// it read and fails if another update came first.
	Version   int64
	CreatedBy uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Limits of the authored fields.
const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
	MaxIdentifierLength  = 255
	MaxSubjects          = 100
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// FieldError is one invalid field of a policy. Code is stable for the API.
type FieldError struct {
	Field string
	Code  string
}

// Field error codes, a subset of the API's closed FieldCode set.
const (
	FieldRequired      = "required"
	FieldInvalidFormat = "invalid_format"
	FieldTooLong       = "too_long"
	FieldOutOfRange    = "out_of_range"
	FieldNotAllowed    = "not_allowed"
)

// InvalidError lists the fields of a policy that cannot be saved.
type InvalidError struct{ Fields []FieldError }

func (e *InvalidError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Code)
	}
	return "invalid data policy: " + strings.Join(parts, ", ")
}

// Normalize lowercases the target's names and trims the authored text, so
// two spellings of one table are one target.
func (p *Policy) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Target.Schema = strings.ToLower(strings.TrimSpace(p.Target.Schema))
	p.Target.Table = strings.ToLower(strings.TrimSpace(p.Target.Table))
	p.Target.Column = strings.ToLower(strings.TrimSpace(p.Target.Column))
	p.Expression = strings.TrimSpace(p.Expression)
	if p.State == "" {
		p.State = StateEnabled
	}
}

// Validate checks the fields a policy must have to be saved. The expression
// itself is checked by Check: an invalid expression is saved as an invalid
// draft, not refused.
func (p *Policy) Validate() error {
	var fields []FieldError
	add := func(field, code string) { fields = append(fields, FieldError{Field: field, Code: code}) }

	switch {
	case p.Name == "":
		add("name", FieldRequired)
	case !namePattern.MatchString(p.Name):
		add("name", FieldInvalidFormat)
	}
	if len(p.Description) > MaxDescriptionLength {
		add("description", FieldTooLong)
	}
	if p.Target.CatalogID == uuid.Nil {
		add("catalog_id", FieldRequired)
	}
	checkIdentifier("schema", p.Target.Schema, add)
	checkIdentifier("table", p.Target.Table, add)
	switch p.State {
	case StateEnabled, StateDisabled:
	default:
		add("state", FieldInvalidFormat)
	}
	if p.Expression == "" {
		add("expression", FieldRequired)
	}

	switch p.Kind {
	case KindRowFilter:
		if p.Mode != ModePermissive && p.Mode != ModeRestrictive {
			add("mode", FieldInvalidFormat)
		}
		if p.Target.Column != "" {
			add("column", FieldNotAllowed)
		}
		if p.Template != "" {
			add("template", FieldNotAllowed)
		}
		if p.Priority != 0 {
			add("priority", FieldNotAllowed)
		}
	case KindColumnMask:
		if p.Mode != "" {
			add("mode", FieldNotAllowed)
		}
		checkIdentifier("column", p.Target.Column, add)
		switch p.Template {
		case "", MaskTemplateNull, MaskTemplateConstant, MaskTemplatePartial, MaskTemplateSHA256, MaskTemplateCustom:
		default:
			add("template", FieldInvalidFormat)
		}
		if p.Priority < 0 {
			add("priority", FieldOutOfRange)
		}
	default:
		add("kind", FieldInvalidFormat)
	}

	if err := validateSubjects(p.Subjects); err != nil {
		add("subjects", err.Error())
	}
	if len(fields) > 0 {
		return &InvalidError{Fields: fields}
	}
	return nil
}

func checkIdentifier(field, value string, add func(field, code string)) {
	switch {
	case value == "":
		add(field, FieldRequired)
	case len(value) > MaxIdentifierLength:
		add(field, FieldTooLong)
	}
}

// errSubject* are the codes of an invalid subject list (FieldError codes).
var (
	errSubjectsRequired = errors.New(FieldRequired)
	errSubjectsTooMany  = errors.New(FieldOutOfRange)
	errSubjectInvalid   = errors.New(FieldInvalidFormat)
)

// validateSubjects checks the subject list: one to MaxSubjects references,
// each naming exactly one thing; Except only under Everyone, and only named
// references there.
func validateSubjects(subjects []Subject) error {
	if len(subjects) == 0 {
		return errSubjectsRequired
	}
	if len(subjects) > MaxSubjects {
		return errSubjectsTooMany
	}
	for _, s := range subjects {
		if s.Everyone {
			if s.User != "" || s.Team != "" || s.Group != "" || s.ServiceAccount != "" {
				return errSubjectInvalid
			}
			if len(s.Except) > MaxSubjects {
				return errSubjectsTooMany
			}
			for _, e := range s.Except {
				if e.Everyone || len(e.Except) > 0 || !validNamed(e) {
					return errSubjectInvalid
				}
			}
			continue
		}
		if len(s.Except) > 0 || !validNamed(s) {
			return errSubjectInvalid
		}
	}
	return nil
}

// validNamed reports whether s names exactly one user, team, group or
// service account, with an id where one is expected.
func validNamed(s Subject) bool {
	set := 0
	for _, v := range []string{s.User, s.Team, s.Group, s.ServiceAccount} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return false
	}
	for _, id := range []string{s.User, s.Team, s.ServiceAccount} {
		if id == "" {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return len(s.Group) <= MaxIdentifierLength
}

// Problem is a reason a set of policies cannot be published.
type Problem struct {
	PolicyID uuid.UUID
	Code     string
	Detail   string
}

// Publish problem codes.
const (
	ProblemInvalid           = "invalid"
	ProblemNotChecked        = "not_checked"
	ProblemDuplicatePriority = "duplicate_priority"
)

// PublishProblems lists why the enabled policies of a set cannot be
// published: an enabled policy whose last check did not pass, and two
// enabled masks with the same priority on one column (OPA cannot pick one
// and Trino then fails every read of the table). Disabled policies are not
// published and are not judged.
func PublishProblems(policies []Policy) []Problem {
	var problems []Problem
	type column struct {
		target   Target
		priority int32
	}
	seen := map[column]uuid.UUID{}
	ordered := slices.Clone(policies)
	slices.SortStableFunc(ordered, func(a, b Policy) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	for _, p := range ordered {
		if p.State != StateEnabled {
			continue
		}
		switch p.Validation {
		case ValidationValid:
		case ValidationInvalid:
			problems = append(problems, Problem{PolicyID: p.ID, Code: ProblemInvalid, Detail: p.InvalidReason})
		default:
			problems = append(problems, Problem{PolicyID: p.ID, Code: ProblemNotChecked})
		}
		if p.Kind != KindColumnMask {
			continue
		}
		key := column{target: p.Target, priority: p.Priority}
		if first, dup := seen[key]; dup {
			problems = append(problems, Problem{
				PolicyID: p.ID, Code: ProblemDuplicatePriority,
				Detail: fmt.Sprintf("priority %d is also used by %s", p.Priority, first),
			})
			continue
		}
		seen[key] = p.ID
	}
	return problems
}
