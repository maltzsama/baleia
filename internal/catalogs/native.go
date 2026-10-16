package catalogs

import (
	"context"

	"github.com/galileostudio/baleia/internal/connections"
)

// NativeNames names the catalogs a direct engine exposes: a PostgreSQL
// session reaches one database; a MySQL server is one catalog named after the
// connection (connName); any other kind lists them through its metadata
// reader. Engine creation and the metadata dispatcher (#17) share it.
func NativeNames(ctx context.Context, p connections.Provider, cfg connections.Config, connName string) ([]string, error) {
	switch p.Kind() {
	case connections.KindPostgreSQL:
		if d := cfg.Value("database"); d != "" {
			return []string{d}, nil
		}
		return []string{"postgres"}, nil
	case connections.KindMySQL, connections.KindMariaDB:
		return []string{connName}, nil
	}
	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	refs, err := r.ListCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(refs))
	for _, c := range refs {
		names = append(names, c.Name)
	}
	return names, nil
}
