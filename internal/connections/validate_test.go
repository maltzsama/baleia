package connections

import (
	"errors"
	"slices"
	"testing"
)

var testSchema = []Field{
	{Key: "url", Type: FieldURL, Required: true},
	{Key: "port", Type: FieldInt, Default: "5432"},
	{Key: "mode", Type: FieldSelect, Options: []string{"a", "b"}},
	{Key: "user", Type: FieldText},
	{Key: "password", Type: FieldSecret, Required: true},
}

func fieldKeys(err error) []string {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	var keys []string
	for _, f := range ve.Fields {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestValidateSchemaNormalizes(t *testing.T) {
	cfg, err := ValidateSchema(testSchema, Config{
		Values:  map[string]string{"url": " https://h/x ", "user": " u "},
		Secrets: map[string]string{"password": " p "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["url"] != "https://h/x" || cfg.Values["user"] != "u" || cfg.Values["port"] != "5432" {
		t.Errorf("values = %v", cfg.Values)
	}
	if cfg.Secrets["password"] != " p " {
		t.Errorf("secrets must not be trimmed: %q", cfg.Secrets["password"])
	}
	if _, ok := cfg.Values["mode"]; ok {
		t.Error("empty optional field stored")
	}
}

func TestValidateSchemaRejects(t *testing.T) {
	cases := map[string]struct {
		cfg  Config
		keys []string
	}{
		"missing required": {Config{Values: map[string]string{}}, []string{"url", "password"}},
		"unknown key":      {Config{Values: map[string]string{"url": "http://h", "x": "1"}, Secrets: map[string]string{"password": "p"}}, []string{"x"}},
		"secret as value":  {Config{Values: map[string]string{"url": "http://h", "password": "p"}}, []string{"password"}},
		"value as secret":  {Config{Values: map[string]string{"url": "http://h"}, Secrets: map[string]string{"password": "p", "user": "u"}}, []string{"user"}},
		"bad types": {Config{
			Values:  map[string]string{"url": "ftp://h", "port": "x", "mode": "c"},
			Secrets: map[string]string{"password": "p"},
		}, []string{"url", "port", "mode"}},
		"url with credentials": {Config{Values: map[string]string{"url": "http://u:p@h"}, Secrets: map[string]string{"password": "p"}}, []string{"url"}},
	}
	for name, c := range cases {
		_, err := ValidateSchema(testSchema, c.cfg)
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
			continue
		}
		got := fieldKeys(err)
		for _, k := range c.keys {
			if !slices.Contains(got, k) {
				t.Errorf("%s: %q not reported in %v", name, k, got)
			}
		}
	}
}
