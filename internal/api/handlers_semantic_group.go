package api

import (
	"github.com/galileostudio/baleia/internal/semantic/docs"
	"github.com/galileostudio/baleia/internal/semantic/metrics"
	"github.com/galileostudio/baleia/internal/semantic/models"
)

// SemanticHandlers serves semantic models and metrics, saved content, versions and bundles (#365).
type SemanticHandlers struct {
	*core
	// docs documents tables and columns (#116).
	docs *docs.Service
	// metrics manages semantic metrics; writes go through content (#34).
	metricDefs *metrics.Service
	// modelDefs manages semantic models; writes go through content (#34).
	modelDefs *models.Service
}

func newSemanticHandlers(c *core) *SemanticHandlers {
	return &SemanticHandlers{core: c, docs: docs.New(c.queries), metricDefs: metrics.New(c.queries, c.contentService()), modelDefs: models.New(c.queries, c.contentService())}
}
