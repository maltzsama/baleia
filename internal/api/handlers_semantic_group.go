package api

import (
	"github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/semantic/docs"
	"github.com/galileostudio/baleia/internal/semantic/metrics"
	"github.com/galileostudio/baleia/internal/semantic/models"
	"github.com/galileostudio/baleia/internal/store/repo"
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
	// search finds content by name or slug (#131).
	search *search.Service
	// glossary holds each workspace's search synonyms (#943); nil without a
	// store.
	glossary *repo.SearchSynonyms
}

func newSemanticHandlers(c *core) *SemanticHandlers {
	var (
		docStore    docs.Store    = c.queries
		metricStore metrics.Store = c.queries
		modelStore  models.Store  = c.queries
		searchStore search.Store  = search.Unindexed{Reader: c.queries}
	)
	if c.store != nil {
		r := repo.New(c.store)
		docStore, metricStore, modelStore, searchStore = r, r, r, r
	}
	var glossary *repo.SearchSynonyms
	if c.store != nil {
		glossary = repo.NewSearchSynonyms(c.store)
	}
	return &SemanticHandlers{
		core:       c,
		glossary:   glossary,
		docs:       docs.New(docStore),
		metricDefs: metrics.New(metricStore, c.contentService()),
		modelDefs:  models.New(modelStore, c.contentService()),
		search:     search.New(searchStore),
	}
}
