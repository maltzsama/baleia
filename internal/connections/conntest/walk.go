// Package conntest holds checks shared by the provider integration tests.
package conntest

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

// CheckWalk checks a reader's one-query catalog walk (#15) against LoadTable:
// every table in want is visited once, with the same type, comment and
// columns that LoadTable reports, and tables arrive ordered by namespace and
// name.
func CheckWalk(t *testing.T, r connections.MetadataReader, catalog string, want ...connections.TableRef) {
	t.Helper()
	ctx := context.Background()
	w, ok := r.(connections.CatalogWalker)
	if !ok {
		t.Fatalf("%T does not implement connections.CatalogWalker", r)
	}
	key := func(ref connections.TableRef) string { return strings.Join(ref.Namespace, "\x1f") + "\x00" + ref.Name }
	wanted := map[string]bool{}
	for _, ref := range want {
		wanted[key(ref)] = true
	}
	got := map[string]connections.TableMeta{}
	var last string
	err := w.WalkCatalog(ctx, catalog, func(m connections.TableMeta) error {
		k := key(m.Ref)
		if last != "" && k <= last {
			t.Errorf("walk out of order: %q after %q", k, last)
		}
		last = k
		if wanted[k] {
			if _, dup := got[k]; dup {
				t.Errorf("table %v visited twice", m.Ref)
			}
			got[k] = m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, ref := range want {
		walked, ok := got[key(ref)]
		if !ok {
			t.Errorf("walk missed %v", ref)
			continue
		}
		loaded, err := r.LoadTable(ctx, ref)
		if err != nil {
			t.Fatalf("load %v: %v", ref, err)
		}
		if walked.Ref.Catalog != catalog || walked.Ref.Type != loaded.Ref.Type || walked.Comment != loaded.Comment || walked.Format != loaded.Format {
			t.Errorf("walk %v = %+v, LoadTable = %+v", ref, walked.Ref, loaded.Ref)
		}
		if !reflect.DeepEqual(slices.Clip(walked.Columns), slices.Clip(loaded.Columns)) {
			t.Errorf("walk columns of %v = %+v, LoadTable = %+v", ref, walked.Columns, loaded.Columns)
		}
	}
}
