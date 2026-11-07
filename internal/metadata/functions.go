package metadata

import (
	"context"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// FunctionReference is one function of an engine's catalog (#788). Signature,
// Description and Kind are empty when the engine does not expose them; nothing
// is invented to fill them.
type FunctionReference struct {
	Name        string
	Signature   string
	Description string
	Kind        string
}

// Functions reads the engine's function catalog in a read-only session, via
// the dialect's FunctionQuery. Rows are name, then optional signature,
// description and kind columns. An engine without a function query yields nil.
func Functions(ctx context.Context, eng queryengine.Engine, catalog string) ([]FunctionReference, error) {
	query := eng.Dialect().FunctionQuery()
	if query == "" {
		return nil, nil
	}
	sess, err := eng.OpenSession(ctx, queryengine.SessionSpec{
		Catalog:  catalog,
		ReadOnly: true,
		Tags:     queryengine.Tags{Source: "functions"},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()

	var out []FunctionReference
	err = sess.Execute(ctx, query, queryengine.Callbacks{}, &rowSink{row: func(r []nullString) error {
		if len(r) == 0 || r[0].null || strings.TrimSpace(r[0].s) == "" {
			return nil
		}
		ref := FunctionReference{Name: r[0].s}
		if len(r) > 1 && !r[1].null {
			ref.Signature = r[1].s
		}
		if len(r) > 2 && !r[2].null {
			ref.Description = r[2].s
		}
		if len(r) > 3 && !r[3].null {
			ref.Kind = r[3].s
		}
		out = append(out, ref)
		return nil
	}})
	if err != nil {
		return nil, fmt.Errorf("list functions: %w", err)
	}
	return out, nil
}
