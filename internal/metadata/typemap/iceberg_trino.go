package typemap

import (
	"regexp"
	"strings"
)

// icebergPrimitives maps Iceberg primitive types to what Trino's Iceberg
// connector shows.
var icebergPrimitives = map[string]string{
	"boolean":        "boolean",
	"int":            "integer",
	"long":           "bigint",
	"float":          "real",
	"double":         "double",
	"date":           "date",
	"time":           "time(6)",
	"timestamp":      "timestamp(6)",
	"timestamptz":    "timestamp(6) with time zone",
	"timestamp_ns":   "timestamp(9)",
	"timestamptz_ns": "timestamp(9) with time zone",
	"string":         "varchar",
	"uuid":           "uuid",
	"binary":         "varbinary",
}

var (
	decimalType = regexp.MustCompile(`^decimal\(\s*(\d+)\s*,\s*(\d+)\s*\)$`)
	fixedType   = regexp.MustCompile(`^fixed\[\d+\]$`)
	plainField  = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// IcebergToTrino renders an Iceberg type as stored by the Iceberg REST reader
// (list<T>, map<K, V>, struct<name: T, ...>) the way Trino shows it.
func IcebergToTrino(native string) (string, bool) {
	p := &parser{s: native}
	out, ok := p.typ()
	if !ok || strings.TrimSpace(p.s[p.i:]) != "" {
		return "", false
	}
	return out, true
}

// parser is a recursive descent over an Iceberg type string.
type parser struct {
	s string
	i int
}

func (p *parser) skip() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *parser) eat(c byte) bool {
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}

// word reads up to the next delimiter of the type grammar. Parentheses and
// brackets belong to the word (decimal(10,2), fixed[16]).
func (p *parser) word() string {
	p.skip()
	start, depth := p.i, 0
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && (c == '<' || c == '>' || c == ',' || c == ':'):
			return strings.TrimSpace(p.s[start:p.i])
		}
		p.i++
	}
	return strings.TrimSpace(p.s[start:p.i])
}

func (p *parser) typ() (string, bool) {
	w := p.word()
	switch w {
	case "list":
		if !p.eat('<') {
			return "", false
		}
		elem, ok := p.typ()
		if !ok || !p.eat('>') {
			return "", false
		}
		return "array(" + elem + ")", true
	case "map":
		if !p.eat('<') {
			return "", false
		}
		k, ok := p.typ()
		if !ok || !p.eat(',') {
			return "", false
		}
		v, ok := p.typ()
		if !ok || !p.eat('>') {
			return "", false
		}
		return "map(" + k + ", " + v + ")", true
	case "struct":
		if !p.eat('<') {
			return "", false
		}
		var fields []string
		for {
			name := p.word()
			if name == "" || !p.eat(':') {
				return "", false
			}
			t, ok := p.typ()
			if !ok {
				return "", false
			}
			fields = append(fields, fieldName(name)+" "+t)
			if p.eat('>') {
				return "row(" + strings.Join(fields, ", ") + ")", true
			}
			if !p.eat(',') {
				return "", false
			}
		}
	}
	if t, ok := icebergPrimitives[w]; ok {
		return t, true
	}
	if m := decimalType.FindStringSubmatch(w); m != nil {
		return "decimal(" + m[1] + ", " + m[2] + ")", true
	}
	if fixedType.MatchString(w) {
		return "varbinary", true
	}
	return "", false
}

// fieldName quotes a row field name unless it is a plain lowercase
// identifier.
func fieldName(n string) string {
	if plainField.MatchString(n) {
		return n
	}
	return `"` + strings.ReplaceAll(n, `"`, `""`) + `"`
}
