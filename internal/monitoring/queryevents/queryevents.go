// Package queryevents ingests the query events Trino's HTTP event listener
// posts (#406). A completed query becomes the trino_query_snapshots row the
// poller would write, as soon as the query ends and with the statistics the
// query list leaves out. The poller keeps running and repairs drift: the
// listener sends no mid-flight progress.
package queryevents

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring/events"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"github.com/google/uuid"
)

// Store writes query snapshots; *repo.Repo implements it.
type Store interface {
	UpsertTrinoQuerySnapshot(ctx context.Context, arg db.UpsertTrinoQuerySnapshotParams) (db.TrinoQuerySnapshot, error)
}

// Engine returns the Baleia engine whose coordinator posts the events: the
// default Trino engine, the one the poller watches.
type Engine func(ctx context.Context) (db.UUID, error)

// maxEventBytes bounds one event: it carries the query's text, plan and
// per-operator statistics.
const maxEventBytes = 32 << 20

// Ingester records query events. It serves the event listener's POSTs.
type Ingester struct {
	store  Store
	engine Engine
	logger *slog.Logger
	// token is what the listener sends as "Authorization: Bearer <token>"
	// (http-event-listener.connect-http-headers).
	token string
	// notify tells the monitoring SSE subscribers a snapshot was recorded
	// (#733); nil skips it (tests).
	notify events.Transport
}

var _ http.Handler = (*Ingester)(nil)

// New builds the ingester for the listener's token; a nil logger discards.
// An empty token serves 404: the feature is off.
func New(logger *slog.Logger, store Store, engine Engine, token string, notify events.Transport) *Ingester {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Ingester{store: store, engine: engine, logger: logger, token: token, notify: notify}
}

// ErrMalformed: the body is not a Trino query event.
var ErrMalformed = errors.New("queryevents: not a trino query event")

// event is the part of a Trino QueryCreatedEvent or QueryCompletedEvent the
// snapshot needs, as Trino 483 serializes it: durations in seconds, sizes in
// bytes. A created event has no statistics. The plans and operator
// statistics are not read, and never stored: they can carry literals.
type event struct {
	Metadata struct {
		QueryID    string `json:"queryId"`
		Query      string `json:"query"`
		QueryState string `json:"queryState"`
	} `json:"metadata"`
	Context struct {
		User    string  `json:"user"`
		Catalog *string `json:"catalog"`
		Schema  *string `json:"schema"`
	} `json:"context"`
	Statistics *struct {
		CPUTime             float64 `json:"cpuTime"`
		WallTime            float64 `json:"wallTime"`
		QueuedTime          float64 `json:"queuedTime"`
		AnalysisTime        float64 `json:"analysisTime"`
		PlanningTime        float64 `json:"planningTime"`
		ExecutionTime       float64 `json:"executionTime"`
		PhysicalInputBytes  int64   `json:"physicalInputBytes"`
		PhysicalInputRows   int64   `json:"physicalInputRows"`
		OutputBytes         int64   `json:"outputBytes"`
		OutputRows          int64   `json:"outputRows"`
		PeakUserMemoryBytes int64   `json:"peakUserMemoryBytes"`
		SpilledDataSize     int64   `json:"spilledDataSize"`
	} `json:"statistics"`
	IO struct {
		Inputs []struct {
			CatalogName string `json:"catalogName"`
			Schema      string `json:"schema"`
			Table       string `json:"table"`
		} `json:"inputs"`
	} `json:"ioMetadata"`
	CreateTime  time.Time `json:"createTime"`
	FailureInfo *struct {
		FailureMessage string `json:"failureMessage"`
		ErrorCode      struct {
			Name string `json:"name"`
		} `json:"errorCode"`
	} `json:"failureInfo"`
}

// Ingest records one event. A created event (no statistics) and a statement
// that is not user activity (monitor.Observable) are accepted and dropped.
func (i *Ingester) Ingest(ctx context.Context, body io.Reader) error {
	var e event
	if err := json.NewDecoder(body).Decode(&e); err != nil || e.Metadata.QueryID == "" {
		return ErrMalformed
	}
	if e.Statistics == nil || !monitor.Observable(e.Metadata.Query) {
		return nil
	}
	engineID, err := i.engine(ctx)
	if err != nil {
		return fmt.Errorf("query events engine: %w", err)
	}
	snap := snapshot(e)
	snap.EngineID = engineID
	if _, err := i.store.UpsertTrinoQuerySnapshot(ctx, snap); err != nil {
		return fmt.Errorf("record query %s: %w", e.Metadata.QueryID, err)
	}
	if err := events.Notify(ctx, i.notify, uuid.UUID(engineID.Bytes).String(), events.KindQueries); err != nil {
		i.logger.WarnContext(ctx, "query event: monitoring notify", "err", err)
	}
	return nil
}

// snapshot maps a completed event to the poller's row. The stored payload is
// the snapshot's own fields, never the event: its plans can carry literals.
func snapshot(e event) db.UpsertTrinoQuerySnapshotParams {
	st := e.Statistics
	elapsed, cpu := millis(st.WallTime), millis(st.CPUTime)
	physical, output := st.PhysicalInputBytes, st.OutputRows
	var errMsg *string
	if f := e.FailureInfo; f != nil {
		msg := f.FailureMessage
		if msg == "" {
			msg = f.ErrorCode.Name
		}
		errMsg = &msg
	}
	created := e.CreateTime
	if created.IsZero() {
		created = time.Now()
	}
	p := db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID:       e.Metadata.QueryID,
		Origin:             "trino_direct",
		User:               e.Context.User,
		State:              monitor.SnapshotState(e.Metadata.QueryState),
		Sql:                e.Metadata.Query,
		Catalog:            e.Context.Catalog,
		Schema:             e.Context.Schema,
		CreatedAt:          db.Timestamptz{Time: created, Valid: true},
		ElapsedMs:          elapsed,
		CpuMs:              cpu,
		PhysicalInputBytes: &physical,
		OutputRows:         &output,
		ErrorMessage:       errMsg,
	}
	p.RawTrinoPayload, _ = json.Marshal(profilePayload{
		Source: "query_completed_event", State: e.Metadata.QueryState,
		QueuedMs: ms64(st.QueuedTime), AnalysisMs: ms64(st.AnalysisTime),
		PlanningMs: ms64(st.PlanningTime), ExecutionMs: ms64(st.ExecutionTime),
		CPUMs: ms64(st.CPUTime), PeakMemoryBytes: st.PeakUserMemoryBytes,
		SpilledBytes: st.SpilledDataSize, InputBytes: st.PhysicalInputBytes,
		InputRows: st.PhysicalInputRows, OutputBytes: st.OutputBytes, OutputRows: st.OutputRows,
		Inputs: inputNames(e.IO.Inputs),
	})
	return p
}

// profilePayload is the snapshot's own execution profile (#532). It is never
// the raw event: that carries plans and literals.
type profilePayload struct {
	Source          string      `json:"source"`
	State           string      `json:"state"`
	QueuedMs        int64       `json:"queuedMs"`
	AnalysisMs      int64       `json:"analysisMs"`
	PlanningMs      int64       `json:"planningMs"`
	ExecutionMs     int64       `json:"executionMs"`
	CPUMs           int64       `json:"cpuMs"`
	PeakMemoryBytes int64       `json:"peakMemoryBytes"`
	SpilledBytes    int64       `json:"spilledBytes"`
	InputBytes      int64       `json:"inputBytes"`
	InputRows       int64       `json:"inputRows"`
	OutputBytes     int64       `json:"outputBytes"`
	OutputRows      int64       `json:"outputRows"`
	Inputs          []inputName `json:"inputs,omitempty"`
}

// inputName is one input table the event names; per-table sizes come from
// /v1/query/{id} when Lens has them.
type inputName struct {
	Catalog string `json:"catalog"`
	Schema  string `json:"schema"`
	Table   string `json:"table"`
}

func inputNames(in []struct {
	CatalogName string `json:"catalogName"`
	Schema      string `json:"schema"`
	Table       string `json:"table"`
}) []inputName {
	out := make([]inputName, 0, len(in))
	for _, i := range in {
		out = append(out, inputName{Catalog: i.CatalogName, Schema: i.Schema, Table: i.Table})
	}
	return out
}

// ms64 converts Trino's seconds to whole milliseconds.
func ms64(seconds float64) int64 {
	return int64(math.Round(seconds * 1000))
}

// millis converts Trino's seconds to whole milliseconds; nil past int32.
func millis(seconds float64) *int32 {
	ms := math.Round(seconds * 1000)
	if ms < 0 || ms > math.MaxInt32 {
		return nil
	}
	v := int32(ms)
	return &v
}

// ServeHTTP serves the event listener's POSTs: only the listener's token is
// accepted, compared in constant time.
func (i *Ingester) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if i.token == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(i.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	err := i.Ingest(r.Context(), http.MaxBytesReader(w, r.Body, maxEventBytes))
	switch {
	case errors.Is(err, ErrMalformed):
		w.WriteHeader(http.StatusBadRequest)
	case err != nil:
		// Logged here only; the listener retries on its own schedule.
		i.logger.ErrorContext(r.Context(), "query event not recorded", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
