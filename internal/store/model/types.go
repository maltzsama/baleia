// Package model is Baleia's store schema: one GORM model per table (ADR 0020).
package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// UUID maps to uuid on PostgreSQL and char(36) on MySQL/MariaDB.
type UUID string

// GormDataType is the name GORM uses when no dialect overrides it.
func (UUID) GormDataType() string { return "uuid" }

// GormDBDataType resolves the column type per dialect.
func (UUID) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Name() == "mysql" {
		return "char(36)"
	}
	return "uuid"
}

// Scan implements sql.Scanner.
func (u *UUID) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*u = ""
	case string:
		*u = UUID(v)
	case []byte:
		*u = UUID(v)
	default:
		return fmt.Errorf("model.UUID: cannot scan %T", value)
	}
	return nil
}

// Value implements driver.Valuer.
func (u UUID) Value() (driver.Value, error) { return string(u), nil }

// JSON maps to jsonb on PostgreSQL and json on MySQL/MariaDB.
type JSON json.RawMessage

// GormDataType is the name GORM uses when no dialect overrides it.
func (JSON) GormDataType() string { return "json" }

// GormDBDataType resolves the column type per dialect.
func (JSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Name() == "mysql" {
		return "json"
	}
	return "jsonb"
}

// Scan implements sql.Scanner.
func (j *JSON) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append((*j)[:0], v...)
	case string:
		*j = JSON(v)
	default:
		return fmt.Errorf("model.JSON: cannot scan %T", value)
	}
	return nil
}

// Value implements driver.Valuer.
func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

// StringArray maps to text[] on PostgreSQL and varchar on MySQL/MariaDB. Both
// store the PostgreSQL array literal, so values round-trip without a second
// representation.
type StringArray []string

// GormDataType is the name GORM uses when no dialect overrides it.
func (StringArray) GormDataType() string { return "string_array" }

// GormDBDataType resolves the column type per dialect. MySQL cannot index or
// default a TEXT column, so it uses a sized varchar.
func (StringArray) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	if db.Name() == "mysql" {
		size := field.Size
		if size == 0 {
			size = 1024
		}
		return fmt.Sprintf("varchar(%d)", size)
	}
	return "text[]"
}

// Scan implements sql.Scanner.
func (a *StringArray) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*a = nil
	case []byte:
		*a = parseStringArray(string(v))
	case string:
		*a = parseStringArray(v)
	default:
		return fmt.Errorf("model.StringArray: cannot scan %T", value)
	}
	return nil
}

// Value implements driver.Valuer.
func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// parseStringArray accepts a PostgreSQL array literal, or a JSON array.
func parseStringArray(s string) StringArray {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil
		}
		return out
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return StringArray{s}
	}
	body := s[1 : len(s)-1]
	if body == "" {
		return StringArray{}
	}
	var (
		out     StringArray
		current strings.Builder
		quoted  bool
		escaped bool
	)
	for _, r := range body {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			out = append(out, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	return append(out, current.String())
}

// Text maps to text on PostgreSQL and varchar on MySQL/MariaDB. MySQL cannot
// index a TEXT column without a prefix length and cannot default one with a
// literal, so indexed or defaulted text columns use this type with a size.
type Text string

// GormDataType is the name GORM uses when no dialect overrides it.
func (Text) GormDataType() string { return "text" }

// GormDBDataType resolves the column type per dialect.
func (Text) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	if db.Name() == "mysql" {
		size := field.Size
		if size == 0 {
			size = 255
		}
		return fmt.Sprintf("varchar(%d)", size)
	}
	return "text"
}

// Scan implements sql.Scanner.
func (t *Text) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*t = ""
	case string:
		*t = Text(v)
	case []byte:
		*t = Text(v)
	default:
		return fmt.Errorf("model.Text: cannot scan %T", value)
	}
	return nil
}

// Value implements driver.Valuer.
func (t Text) Value() (driver.Value, error) { return string(t), nil }

// Inet maps to inet on PostgreSQL and varchar(45) on MySQL/MariaDB.
type Inet string

// GormDataType is the name GORM uses when no dialect overrides it.
func (Inet) GormDataType() string { return "inet" }

// GormDBDataType resolves the column type per dialect.
func (Inet) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Name() == "mysql" {
		return "varchar(45)"
	}
	return "inet"
}

// Scan implements sql.Scanner.
func (i *Inet) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*i = ""
	case string:
		*i = Inet(v)
	case []byte:
		*i = Inet(v)
	default:
		return fmt.Errorf("model.Inet: cannot scan %T", value)
	}
	return nil
}

// Value implements driver.Valuer.
func (i Inet) Value() (driver.Value, error) { return string(i), nil }
