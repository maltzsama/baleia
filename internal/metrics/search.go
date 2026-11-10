package metrics

import "github.com/prometheus/client_golang/prometheus"

// Search surfaces, the kind label of baleia_search_requests_total.
const (
	SearchMetadata = "metadata"
	SearchDocs     = "docs"
	SearchContent  = "content"
	// SearchFederated is the one search over every kind (#943).
	SearchFederated = "federated"
)

// Search counts in-product search requests (#941).
type Search struct {
	requests *prometheus.CounterVec
}

// Search registers baleia_search_requests_total{kind,outcome}: kind is the
// surface (metadata, docs or content) and outcome is hit or empty. The query
// text is never a label.
//
// Zero-result rate by surface:
//
//	sum by (kind) (rate(baleia_search_requests_total{outcome="empty"}[1h]))
//	  / sum by (kind) (rate(baleia_search_requests_total[1h]))
func (r *Registry) Search() *Search {
	s := &Search{requests: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "search_requests_total",
		Help:      "In-product search requests by surface and whether they found anything.",
	}, []string{"kind", "outcome"})}
	r.reg.MustRegister(s.requests)
	return s
}

// Result records one search of surface that returned hits results.
func (s *Search) Result(surface string, hits int) {
	if s == nil {
		return
	}
	outcome := "hit"
	if hits == 0 {
		outcome = "empty"
	}
	s.requests.WithLabelValues(surface, outcome).Inc()
}
