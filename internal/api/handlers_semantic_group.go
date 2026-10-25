package api

import "github.com/galileostudio/baleia/internal/semantic/docs"

// SemanticHandlers serves semantic models and metrics, saved content, versions and bundles (#365).
type SemanticHandlers struct {
	*core
	// docs documents tables and columns (#116).
	docs *docs.Service
}

func newSemanticHandlers(c *core) *SemanticHandlers {
	return &SemanticHandlers{core: c, docs: docs.New(c.queries)}
}
