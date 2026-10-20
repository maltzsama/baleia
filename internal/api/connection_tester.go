package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Connection tests (#12): the same checks for settings not saved yet and for
// a saved connection. Nothing is persisted, except the registry row of a
// bind check's short-lived catalog, which is always dropped (and swept if a
// crash leaves it).

const (
	checkTimeout      = 10 * time.Second
	bindCheckTimeout  = 15 * time.Second
	ephemeralTTL      = 2 * time.Minute
	ephemeralDropTime = 10 * time.Second
)

// Bounded error codes of a connection test.
const (
	codeUnreachable         = "unreachable"
	codeTLS                 = "tls"
	codeAuthFailed          = "auth_failed"
	codeForbidden           = "forbidden"
	codeNotFound            = "not_found"
	codeTimeout             = "timeout"
	codeProviderUnsupported = "provider_unsupported"
	codeDeniedNetwork       = "connection_denied_network"
	codeEngineUnavailable   = "engine_unavailable"
	codeInternal            = "internal"
)

var codeMessages = map[string]string{
	codeUnreachable:         "the host could not be reached",
	codeTLS:                 "the TLS handshake failed",
	codeAuthFailed:          "the credentials were refused",
	codeForbidden:           "the credentials lack the needed permission",
	codeNotFound:            "the catalog, database or endpoint does not exist",
	codeTimeout:             "the check timed out",
	codeProviderUnsupported: "this connection type cannot be tested",
	codeDeniedNetwork:       "the destination is not an allowed network address",
	codeEngineUnavailable:   "the engine could not run the check",
	codeInternal:            "the check failed",
}

// classifyTestError maps a check's error to a bounded code. The raw error is
// logged by the caller, never returned.
func classifyTestError(err error) string {
	var se *icebergrest.StatusError
	var pgErr *pgconn.PgError
	var netErr net.Error
	var qe *queryengine.Error
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, connections.ErrDeniedNetwork):
		return codeDeniedNetwork
	case errors.Is(err, connections.ErrUnknownKind), errors.Is(err, queryengine.ErrKindNotRegistered):
		return codeProviderUnsupported
	case errors.Is(err, context.DeadlineExceeded):
		return codeTimeout
	case errors.As(err, &se):
		switch se.Status {
		case 401:
			return codeAuthFailed
		case 403:
			return codeForbidden
		case 404:
			return codeNotFound
		}
		return codeEngineUnavailable
	case errors.Is(err, connections.ErrNotFound):
		return codeNotFound
	case errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "28"):
		return codeAuthFailed
	case errors.As(err, &pgErr) && pgErr.Code == "3D000":
		return codeNotFound
	case errors.As(err, &qe):
		switch qe.Code {
		case queryengine.CodePermission:
			return codeForbidden
		case queryengine.CodeNotFound:
			return codeNotFound
		case queryengine.CodeTimeout:
			return codeTimeout
		}
		if strings.Contains(msg, "authentication") || strings.Contains(msg, "password") || strings.Contains(msg, "access denied") {
			return codeAuthFailed
		}
		return codeEngineUnavailable
	case strings.Contains(msg, "x509") || strings.Contains(msg, "tls:") || strings.Contains(msg, "certificate"):
		return codeTLS
	case strings.Contains(msg, "password authentication failed"), strings.Contains(msg, "access denied for user"),
		strings.Contains(msg, "authentication failed"), strings.Contains(msg, "login failed"), strings.Contains(msg, "invalid_client"),
		strings.Contains(msg, "ora-01017"):
		return codeAuthFailed
	case errors.As(err, &netErr) && netErr.Timeout():
		return codeTimeout
	case errors.As(err, &netErr), strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"),
		strings.Contains(msg, "no route to host"), strings.Contains(msg, "connect:"):
		return codeUnreachable
	}
	return codeInternal
}

// testBind is a federated engine a bind check runs through.
type testBind struct {
	engine  db.Engine
	cluster db.TrinoCluster
}

// connectionTest runs the checks the intents select on one connection's
// settings.
type connectionTest struct {
	h        *Handlers
	provider connections.Provider
	cfg      connections.Config
	// connName names a MySQL server's only native catalog.
	connName string
	metadata bool
	direct   bool
	binds    []testBind
}

func ptr[T any](v T) *T { return &v }

// run executes the checks in order and stops at the first failure.
func (t connectionTest) run(ctx context.Context) TestCatalogConnectionResult {
	start := time.Now()
	res := TestCatalogConnectionResult{Success: true, Checks: &[]ConnectionCheck{}}
	add := func(c ConnectionCheck, err error) bool {
		if err != nil {
			code := classifyTestError(err)
			slog.Info("connection test check failed", "check", c.Name, "code", code, "request_id", middleware.GetReqID(ctx), "err", err)
			c.Ok, c.ErrorCode = false, &code
			if res.Success {
				res.Success = false
				ec := TestCatalogConnectionResultErrorCode(code)
				msg := codeMessages[code]
				res.ErrorCode, res.ErrorMessage, res.Error = &ec, &msg, ptr(code+": "+msg)
			}
		} else {
			c.Ok = true // a skipped check did not fail
		}
		*res.Checks = append(*res.Checks, c)
		return err == nil
	}
	done := func() TestCatalogConnectionResult {
		res.LatencyMs = int(time.Since(start).Milliseconds())
		return res
	}

	if t.metadata {
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		err := t.provider.Ping(cctx, t.cfg)
		cancel()
		if !add(ConnectionCheck{Name: "ping"}, err) {
			return done()
		}
		if slices.Contains(t.provider.Roles(), connections.RoleMetadata) {
			n, err := t.listCatalogs(ctx)
			c := ConnectionCheck{Name: "list_catalogs"}
			if err == nil {
				c.Detail = ptr(fmt.Sprintf("%d catalogs", n))
			}
			if !add(c, err) {
				return done()
			}
		}
	}
	if t.direct {
		c, err := t.directEngine(ctx)
		if !add(c, err) {
			return done()
		}
	}
	for _, b := range t.binds {
		if !add(ConnectionCheck{Name: "bind:" + b.engine.Name}, t.bind(ctx, b)) {
			return done()
		}
	}
	return done()
}

func (t connectionTest) listCatalogs(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	r, err := t.provider.OpenMetadata(ctx, t.cfg)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	cats, err := r.ListCatalogs(ctx)
	return len(cats), err
}

// directEngine runs one trivial query through a transient engine instance
// of the provider's engine kind: never registered, closed after the check.
// A kind without an adapter yet is skipped.
func (t connectionTest) directEngine(ctx context.Context) (ConnectionCheck, error) {
	c := ConnectionCheck{Name: "direct_engine"}
	kind := t.provider.EngineKind()
	if !slices.Contains(queryengine.Kinds(), kind) {
		c.Skipped, c.Detail = ptr(true), ptr("no "+kind+" engine adapter yet")
		return c, nil
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	eng, err := queryengine.Transient(ctx, kind, t.cfg, t.h.dialer)
	if err != nil {
		return c, err
	}
	defer func() { _ = eng.Close() }()
	if err := eng.Ping(ctx); err != nil {
		return c, err
	}
	probe := "SELECT 1"
	if kind == "oracle" {
		probe = "SELECT 1 FROM dual"
	}
	if _, err := queryengine.CollectStrings(ctx, eng, queryengine.SessionSpec{ReadOnly: true, Tags: queryengine.Tags{Source: "connection-test"}}, probe, 1); err != nil {
		return c, err
	}
	if v, ok := queryengine.As[queryengine.Versioner](eng); ok {
		if version, err := v.Version(ctx); err == nil {
			c.Detail = &version
		}
	}
	return c, nil
}

// bind binds a short-lived catalog of the settings into the federated
// engine, lists its schemas through that engine, and always drops it. The
// drop never uses the request context.
func (t connectionTest) bind(ctx context.Context, b testBind) error {
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	name := bindtrino.EphemeralPrefix + hex.EncodeToString(suffix)
	// No source ref: a database's catalog reaches the connection's own database.
	proj, err := t.h.binder.Project(bindings.BindingSpec{EngineCatalog: name}, t.cfg)
	if err != nil {
		return err
	}
	ref := bindings.EngineRef{ID: b.engine.ID, ClusterID: b.cluster.ID}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ephemeralDropTime)
		defer cancel()
		if err := t.h.binder.DropEphemeral(dctx, t.h.queries, ref, name); err != nil {
			slog.Warn("drop test catalog; the sweeper will retry", "catalog", name, "err", err)
		}
	}()
	bctx, cancel := context.WithTimeout(ctx, bindCheckTimeout)
	defer cancel()
	if err := t.h.binder.CreateEphemeral(bctx, t.h.queries, ref, proj, ephemeralTTL); err != nil {
		return err
	}
	eng, err := t.h.engines.Get(bctx, b.engine.ID)
	if err != nil {
		return err
	}
	target, err := eng.Dialect().QualifiedName(name)
	if err != nil {
		return err
	}
	spec := queryengine.SessionSpec{Tags: queryengine.Tags{Source: "connection-test", RequestID: middleware.GetReqID(ctx)}}
	_, err = queryengine.CollectStrings(bctx, eng, spec, "SHOW SCHEMAS FROM "+target, 10000)
	return err
}

// plan turns intents into checks. With no intents: the provider's roles,
// and a bind check on the default Trino engine for a database the binder
// supports. A bind: intent names an engine id the caller may bind into;
// anything else is reported as not found.
func (h *Handlers) planTest(ctx context.Context, p authz.Principal, provider connections.Provider, cfg connections.Config, connName string, intents *[]string) (connectionTest, *string, error) {
	t := connectionTest{h: h, provider: provider, cfg: cfg, connName: connName}
	roles := provider.Roles()
	if intents == nil || len(*intents) == 0 {
		t.metadata = true
		t.direct = slices.Contains(roles, connections.RoleDirect)
		if (provider.Family() == connections.FamilyDatabase || provider.Family() == connections.FamilyMetastore) && h.binder != nil && len(h.binder.Supports()[provider.Kind()]) > 0 {
			if cluster, err := h.queries.GetDefaultTrinoCluster(ctx); err == nil && cluster.EngineID.Valid {
				if eng, err := h.queries.GetEngineByID(ctx, cluster.EngineID); err == nil && eng.Enabled {
					t.binds = append(t.binds, testBind{engine: eng, cluster: cluster})
				}
			}
		}
		return t, nil, nil
	}
	for _, in := range *intents {
		switch {
		case in == "metadata":
			t.metadata = true
		case in == "direct_engine":
			t.direct = true
		case strings.HasPrefix(in, "bind:"):
			var id pgtype.UUID
			if err := id.Scan(strings.TrimPrefix(in, "bind:")); err != nil {
				return t, ptr("engine not found"), nil
			}
			eng, _, err := h.authz().Engine(ctx, p, id, authz.ActWrite)
			if errors.Is(err, authz.ErrNotFound) || errors.Is(err, authz.ErrForbidden) {
				return t, ptr("engine not found"), nil
			}
			if err != nil {
				return t, nil, err
			}
			cluster, err := h.queries.GetTrinoClusterByEngine(ctx, eng.ID)
			if err != nil || h.binder == nil || eng.Kind != h.binder.EngineKind() {
				return t, ptr("engine not found"), nil
			}
			t.binds = append(t.binds, testBind{engine: eng, cluster: cluster})
		default:
			return t, ptr("unknown intent " + in), nil
		}
	}
	return t, nil, nil
}

// testLimit allows each user testLimitPerMinute connection tests per
// minute: the endpoint takes credentials and dials out from Baleia.
const testLimitPerMinute = 10

type testLimiter struct {
	mu      sync.Mutex
	windows map[string]testWindow
}

type testWindow struct {
	start time.Time
	n     int
}

var connectionTests = &testLimiter{windows: map[string]testWindow{}}

// allow counts one test of user in the current minute.
func (l *testLimiter) allow(user string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[user]
	if now.Sub(w.start) >= time.Minute {
		w = testWindow{start: now}
	}
	if w.n >= testLimitPerMinute {
		return false
	}
	w.n++
	l.windows[user] = w
	return true
}

// TestConnectionDraft — POST /catalog-connections/test (#12): the checks on
// settings not saved yet. Nothing is persisted.
func (h *Handlers) TestConnectionDraft(ctx context.Context, request TestConnectionDraftRequestObject) (TestConnectionDraftResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return TestConnectionDraft401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if !connectionTests.allow(p.UserID.String(), time.Now()) {
		return TestConnectionDraft429JSONResponse{Code: ErrorCodeRateLimited, Message: "too many connection tests; try again in a minute"}, nil
	}
	if request.Body == nil {
		return TestConnectionDraft422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	// The draft is a create payload; its id only seals nothing.
	raw, err := json.Marshal(request.Body)
	if err != nil {
		return nil, err
	}
	var draft CreateCatalogConnectionRequest
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, err
	}
	cfg, err := h.createConfig(pgtype.UUID{Bytes: uuid.New(), Valid: true}, &draft)
	var ce *configError
	if errors.As(err, &ce) {
		return TestConnectionDraft422JSONResponse{UnprocessableEntityJSONResponse: ce.response()}, nil
	}
	if err != nil {
		return nil, err
	}
	provider, err := h.connections.Get(cfg.Kind)
	if err != nil {
		return TestConnectionDraft422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(codeProviderUnsupported, "unknown connection type")}, nil
	}
	t, refused, err := h.planTest(ctx, p, provider, cfg, draft.Name, request.Body.Intents)
	if err != nil {
		return nil, err
	}
	if refused != nil {
		if strings.HasPrefix(*refused, "unknown intent") {
			return TestConnectionDraft422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, *refused)}, nil
		}
		return TestConnectionDraft404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: *refused}}, nil
	}
	return TestConnectionDraft200JSONResponse(t.run(ctx)), nil
}
