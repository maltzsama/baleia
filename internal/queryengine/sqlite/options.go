package sqlite

// Options are the server-side SQLite settings, mirroring the provider's
// (docs/adr/0011): Root is BALEIA_SQLITE_ROOT, which confines every database
// file. The composition root passes them to Spec (#364).
type Options struct {
	Root string
}
