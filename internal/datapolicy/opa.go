package datapolicy

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EngineLabel is the OPA label that names the engine an OPA serves. The
// deployment sets it in OPA's configuration (labels.baleia_engine_id).
const EngineLabel = "baleia_engine_id"

// Paths of the OPA-facing endpoints, outside /api/v1.
const (
	BundlePath = "/opa/bundles/baleia.tar.gz"
	StatusPath = "/opa/status"
)

// maxStatusBytes bounds a status report.
const maxStatusBytes = 1 << 20

// OPAStore is what the OPA endpoints read and write.
type OPAStore interface {
	PublishedRevision(ctx context.Context) (Revision, error)
	RecordEngineStatus(ctx context.Context, s EngineStatus) error
}

// OPAHandler serves OPA's bundle downloads and status reports (ADR 0028).
// Every replica serves the published revision's stored bundle, with the
// revision as its ETag; no replica builds or caches one.
type OPAHandler struct {
	Store   OPAStore
	Objects ObjectStore
	// Token is the bearer token OPA sends; empty turns the endpoints off.
	Token string
	// EngineExists reports whether a reporting engine is known.
	EngineExists func(ctx context.Context, id uuid.UUID) (bool, error)
	// DefaultEngineAlias is BALEIA_DEFAULT_ENGINE_ID: the id the default Trino
	// engine's OPA reports under. The chart generates it before any engine
	// exists, so it is not the engine's own id; a report under it is the
	// default engine's (DefaultEngine). uuid.Nil turns the alias off.
	DefaultEngineAlias uuid.UUID
	// DefaultEngine returns the default Trino engine's id, or ErrNotFound.
	DefaultEngine func(ctx context.Context) (uuid.UUID, error)
	Logger        *slog.Logger
	Now           func() time.Time
}

func (h *OPAHandler) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && h.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) == 1
}

func (h *OPAHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// ServeBundle streams the published revision's bundle. OPA sends the ETag it
// holds as If-None-Match and gets 304 while the revision is unchanged; it
// gets 404 while nothing is published, and keeps denying users.
func (h *OPAHandler) ServeBundle(w http.ResponseWriter, r *http.Request) {
	if h.Token == "" {
		http.NotFound(w, r)
		return
	}
	if !h.authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rev, err := h.Store.PublishedRevision(r.Context())
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.logger().ErrorContext(r.Context(), "read the published data policy revision", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	etag := `"` + rev.ID.String() + `"`
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match == etag || match == rev.ID.String() {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, _, err := h.Objects.GetReader(r.Context(), BundleKey(rev.ID))
	if err != nil {
		h.logger().ErrorContext(r.Context(), "read the published data policy bundle", "revision", rev.ID, "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Content-Type", bundleContentType)
	if _, err := io.Copy(w, body); err != nil {
		h.logger().WarnContext(r.Context(), "stream the data policy bundle", "revision", rev.ID, "err", err)
	}
}

// statusReport is the part of OPA's status report Baleia reads.
type statusReport struct {
	Labels  map[string]string `json:"labels"`
	Bundles map[string]struct {
		ActiveRevision string `json:"active_revision"`
		Code           string `json:"code"`
		Message        string `json:"message"`
	} `json:"bundles"`
}

// ReceiveStatus records an OPA's status report for the engine its label
// names: the data bundle revision it activated, the policy bundle's digest
// (its manifest revision) and the data bundle's activation error.
func (h *OPAHandler) ReceiveStatus(w http.ResponseWriter, r *http.Request) {
	if h.Token == "" {
		http.NotFound(w, r)
		return
	}
	if !h.authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var report statusReport
	if err := json.NewDecoder(io.LimitReader(r.Body, maxStatusBytes)).Decode(&report); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	engineID, err := uuid.Parse(report.Labels[EngineLabel])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if h.DefaultEngineAlias != uuid.Nil && engineID == h.DefaultEngineAlias && h.DefaultEngine != nil {
		def, err := h.DefaultEngine(r.Context())
		if errors.Is(err, ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err != nil {
			h.logger().ErrorContext(r.Context(), "look up the default trino engine", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		engineID = def
	}
	known, err := h.EngineExists(r.Context(), engineID)
	if err != nil {
		h.logger().ErrorContext(r.Context(), "look up the reporting engine", "engine_id", engineID, "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	data, policy := report.Bundles["baleia"], report.Bundles["policy"]
	status := EngineStatus{
		EngineID: engineID, ReportedAt: now, ActiveRevision: data.ActiveRevision,
		PolicyDigest: policy.ActiveRevision, Code: data.Code, Message: data.Message,
	}
	if err := h.Store.RecordEngineStatus(r.Context(), status); err != nil {
		h.logger().ErrorContext(r.Context(), "record an engine's OPA status", "engine_id", engineID, "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
