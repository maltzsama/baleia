package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

// HTTP is the API's request metrics.
type HTTP struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inflight prometheus.Gauge
}

// HTTP registers baleia_http_requests_total, baleia_http_request_duration_seconds
// and baleia_http_inflight_requests.
func (r *Registry) HTTP() *HTTP {
	m := &HTTP{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "http_requests_total", Help: "HTTP requests by route template, method and status class.",
		}, []string{"route", "method", "status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "http_request_duration_seconds", Help: "HTTP request duration by route template.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"route", "method"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "http_inflight_requests", Help: "HTTP requests being served.",
		}),
	}
	r.reg.MustRegister(m.requests, m.duration, m.inflight)
	return m
}

// knownMethods bounds the method label; anything else is "other".
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// Route is the chi route template a request matched — never the raw path,
// which carries ids — or "unmatched".
func Route(r *http.Request) string {
	rc := chi.RouteContext(r.Context())
	if rc == nil {
		return "unmatched"
	}
	if p := rc.RoutePattern(); p != "" {
		return p
	}
	return "unmatched"
}

// Middleware records every request; install it on the root router.
func (m *HTTP) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inflight.Inc()
		defer m.inflight.Dec()
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		method := r.Method
		if !knownMethods[method] {
			method = "other"
		}
		route := Route(r)
		if status == http.StatusNotFound && strings.HasSuffix(route, "/*") {
			route = "unmatched" // a group's catch-all 404, not a route
		}
		m.requests.WithLabelValues(route, method, strconv.Itoa(status/100)+"xx").Inc()
		m.duration.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
	})
}
