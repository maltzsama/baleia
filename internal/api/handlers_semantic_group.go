package api

// SemanticHandlers serves semantic models and metrics, saved content, versions and bundles (#365).
type SemanticHandlers struct {
	*core
}

func newSemanticHandlers(c *core) *SemanticHandlers { return &SemanticHandlers{core: c} }
