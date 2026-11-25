package datapolicy

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validRowFilter() Policy {
	return Policy{
		Name:       "eu-only",
		Kind:       KindRowFilter,
		Target:     Target{CatalogID: uuid.New(), Schema: "Sales", Table: " Orders "},
		Mode:       ModePermissive,
		Subjects:   []Subject{{Team: uuid.NewString()}},
		Expression: " region = 'EU' ",
	}
}

func validMask() Policy {
	return Policy{
		Name:       "hide-email",
		Kind:       KindColumnMask,
		Target:     Target{CatalogID: uuid.New(), Schema: "sales", Table: "orders", Column: "EMAIL"},
		Priority:   3,
		Subjects:   []Subject{{Everyone: true, Except: []Subject{{Group: "auditors"}}}},
		Expression: "'***'",
		Template:   MaskTemplateConstant,
	}
}

func TestPolicy_Writes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		kind Kind
		want Writes
	}{
		{"row filter", KindRowFilter, Writes{Insert: WriteChecked, Update: WriteRefused, Delete: WriteVisibleRows, Merge: WriteRefused}},
		{"column mask", KindColumnMask, Writes{Insert: WriteRefused, Update: WriteRefused, Delete: WriteRefused, Merge: WriteRefused}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (Policy{Kind: tt.kind}).Writes(); got != tt.want {
				t.Fatalf("Writes() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPolicy_Normalize(t *testing.T) {
	t.Parallel()
	p := validRowFilter()
	p.Normalize()
	if p.Target.Schema != "sales" || p.Target.Table != "orders" || p.Expression != "region = 'EU'" || p.State != StateEnabled {
		t.Fatalf("Normalize = %+v", p)
	}
	m := validMask()
	m.Normalize()
	if m.Target.Column != "email" {
		t.Fatalf("Normalize column = %q, want email", m.Target.Column)
	}
}

func TestPolicy_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		base     func() Policy
		mutate   func(*Policy)
		expected []FieldError
	}{
		{name: "valid row filter", base: validRowFilter, mutate: func(*Policy) {}},
		{name: "valid mask", base: validMask, mutate: func(*Policy) {}},
		{name: "missing name", base: validRowFilter, mutate: func(p *Policy) { p.Name = "" }, expected: []FieldError{{"name", FieldRequired}}},
		{name: "bad name", base: validRowFilter, mutate: func(p *Policy) { p.Name = "Has Space" }, expected: []FieldError{{"name", FieldInvalidFormat}}},
		{name: "long description", base: validRowFilter, mutate: func(p *Policy) { p.Description = strings.Repeat("x", MaxDescriptionLength+1) }, expected: []FieldError{{"description", FieldTooLong}}},
		{name: "no catalog", base: validRowFilter, mutate: func(p *Policy) { p.Target.CatalogID = uuid.Nil }, expected: []FieldError{{"catalog_id", FieldRequired}}},
		{name: "no table", base: validRowFilter, mutate: func(p *Policy) { p.Target.Table = "" }, expected: []FieldError{{"table", FieldRequired}}},
		{name: "long schema", base: validRowFilter, mutate: func(p *Policy) { p.Target.Schema = strings.Repeat("s", MaxIdentifierLength+1) }, expected: []FieldError{{"schema", FieldTooLong}}},
		{name: "unknown state", base: validRowFilter, mutate: func(p *Policy) { p.State = "paused" }, expected: []FieldError{{"state", FieldInvalidFormat}}},
		{name: "no expression", base: validRowFilter, mutate: func(p *Policy) { p.Expression = "" }, expected: []FieldError{{"expression", FieldRequired}}},
		{name: "row filter without mode", base: validRowFilter, mutate: func(p *Policy) { p.Mode = "" }, expected: []FieldError{{"mode", FieldInvalidFormat}}},
		{name: "row filter with a column", base: validRowFilter, mutate: func(p *Policy) { p.Target.Column = "email" }, expected: []FieldError{{"column", FieldNotAllowed}}},
		{name: "row filter with a template", base: validRowFilter, mutate: func(p *Policy) { p.Template = MaskTemplateNull }, expected: []FieldError{{"template", FieldNotAllowed}}},
		{name: "row filter with a priority", base: validRowFilter, mutate: func(p *Policy) { p.Priority = 1 }, expected: []FieldError{{"priority", FieldNotAllowed}}},
		{name: "mask with a mode", base: validMask, mutate: func(p *Policy) { p.Mode = ModeRestrictive }, expected: []FieldError{{"mode", FieldNotAllowed}}},
		{name: "mask without a column", base: validMask, mutate: func(p *Policy) { p.Target.Column = "" }, expected: []FieldError{{"column", FieldRequired}}},
		{name: "mask with an unknown template", base: validMask, mutate: func(p *Policy) { p.Template = "hmac" }, expected: []FieldError{{"template", FieldInvalidFormat}}},
		{name: "mask with a negative priority", base: validMask, mutate: func(p *Policy) { p.Priority = -1 }, expected: []FieldError{{"priority", FieldOutOfRange}}},
		{name: "unknown kind", base: validRowFilter, mutate: func(p *Policy) { p.Kind = "both" }, expected: []FieldError{{"kind", FieldInvalidFormat}}},
		{name: "no subjects", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = nil }, expected: []FieldError{{"subjects", FieldRequired}}},
		{name: "too many subjects", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = make([]Subject, MaxSubjects+1) }, expected: []FieldError{{"subjects", FieldOutOfRange}}},
		{name: "subject naming two things", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = []Subject{{Team: uuid.NewString(), Group: "g"}} }, expected: []FieldError{{"subjects", FieldInvalidFormat}}},
		{name: "empty subject", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = []Subject{{}} }, expected: []FieldError{{"subjects", FieldInvalidFormat}}},
		{name: "user that is not an id", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = []Subject{{User: "ana"}} }, expected: []FieldError{{"subjects", FieldInvalidFormat}}},
		{name: "except outside everyone", base: validRowFilter, mutate: func(p *Policy) {
			p.Subjects = []Subject{{Team: uuid.NewString(), Except: []Subject{{Group: "g"}}}}
		}, expected: []FieldError{{"subjects", FieldInvalidFormat}}},
		{name: "everyone with a name", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = []Subject{{Everyone: true, Group: "g"}} }, expected: []FieldError{{"subjects", FieldInvalidFormat}}},
		{name: "nested everyone", base: validRowFilter, mutate: func(p *Policy) {
			p.Subjects = []Subject{{Everyone: true, Except: []Subject{{Everyone: true}}}}
		}, expected: []FieldError{{"subjects", FieldInvalidFormat}}},
		{name: "service account", base: validRowFilter, mutate: func(p *Policy) { p.Subjects = []Subject{{ServiceAccount: uuid.NewString()}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.base()
			p.Normalize()
			tt.mutate(&p)
			err := p.Validate()
			if tt.expected == nil {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("Validate = %v, want an *InvalidError", err)
			}
			if !slices.Equal(invalid.Fields, tt.expected) {
				t.Fatalf("Validate fields = %v, want %v", invalid.Fields, tt.expected)
			}
		})
	}
}

func TestPublishProblems(t *testing.T) {
	t.Parallel()
	table := Target{CatalogID: uuid.New(), Schema: "sales", Table: "orders", Column: "email"}
	mask := func(priority int32, validation Validation, state State) Policy {
		return Policy{ID: uuid.New(), Kind: KindColumnMask, Target: table, Priority: priority, Validation: validation, State: state}
	}
	first := mask(5, ValidationValid, StateEnabled)
	sameColumnSamePriority := mask(5, ValidationValid, StateEnabled)
	disabledSamePriority := mask(5, ValidationValid, StateDisabled)
	otherPriority := mask(6, ValidationValid, StateEnabled)
	otherColumn := mask(5, ValidationValid, StateEnabled)
	otherColumn.Target.Column = "phone"
	invalid := Policy{ID: uuid.New(), Kind: KindRowFilter, Validation: ValidationInvalid, InvalidReason: "not_boolean", State: StateEnabled}
	pending := Policy{ID: uuid.New(), Kind: KindRowFilter, Validation: ValidationPending, State: StateEnabled}
	disabledInvalid := Policy{ID: uuid.New(), Kind: KindRowFilter, Validation: ValidationInvalid, State: StateDisabled}

	problems := PublishProblems([]Policy{first, sameColumnSamePriority, disabledSamePriority, otherPriority, otherColumn, invalid, pending, disabledInvalid})
	got := map[uuid.UUID]string{}
	for _, p := range problems {
		got[p.PolicyID] = p.Code
	}
	if len(got) != 3 || got[invalid.ID] != ProblemInvalid || got[pending.ID] != ProblemNotChecked {
		t.Fatalf("PublishProblems = %+v", problems)
	}
	// Exactly one of the two enabled masks with priority 5 on email is flagged.
	if (got[first.ID] == ProblemDuplicatePriority) == (got[sameColumnSamePriority.ID] == ProblemDuplicatePriority) {
		t.Fatalf("duplicate priority not reported once: %+v", problems)
	}
	if PublishProblems([]Policy{first, otherPriority, otherColumn}) != nil {
		t.Fatal("distinct priorities reported a problem")
	}
}
