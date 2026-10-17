package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

const (
	// engineVersionTTL is how long an engine's reported version is reused.
	engineVersionTTL = 5 * time.Minute
	// engineVersionTimeout bounds asking an engine for its version, so a list
	// never waits long on an unreachable engine (failures are cached too).
	engineVersionTimeout = 2 * time.Second
	// describeParallelism bounds how many engines a list describes at once:
	// cold version lookups overlap instead of adding up.
	describeParallelism = 8
	// maxSplitSQL is SplitStatementsRequest.sql's maxLength, in characters.
	maxSplitSQL = 1 << 20
)

// describeEngines describes every engine, in order, at most
// describeParallelism at a time.
func (h *Handlers) describeEngines(ctx context.Context, engines []db.Engine) []Engine {
	out := make([]Engine, len(engines))
	sem := make(chan struct{}, describeParallelism)
	var wg sync.WaitGroup
	for i, e := range engines {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			out[i] = h.describeEngine(ctx, e)
		}()
	}
	wg.Wait()
	return out
}

func dialectToPublic(d dialect.Dialect) *EngineDialect {
	return &EngineDialect{Name: d.Name(), FormatterLanguage: d.FormatterLanguage(), CodemirrorBase: d.CodeMirrorBase()}
}

func capabilitiesToPublic(c queryengine.Capabilities) *EngineCapabilities {
	modes := make([]EngineCapabilitiesIdentityModes, 0, len(c.IdentityModes))
	for _, m := range c.IdentityModes {
		modes = append(modes, EngineCapabilitiesIdentityModes(m))
	}
	out := &EngineCapabilities{
		Dialect:             c.Dialect,
		IdentityModes:       modes,
		ReadOnly:            EngineCapabilitiesReadOnly(c.ReadOnly),
		SupportsCancel:      c.SupportsCancel,
		Transactions:        c.Transactions,
		MultipleResultSets:  c.MultipleResultSets,
		MultipleCatalogs:    c.MultipleCatalogs,
		NamespaceDepth:      c.NamespaceDepth,
		SessionSchema:       c.SessionSchema,
		SupportsExplain:     c.SupportsExplain,
		SupportsTimeTravel:  c.SupportsTimeTravel,
		SupportsCatalogDdl:  c.SupportsCatalogDDL,
		Monitoring:          c.Monitoring,
		IcebergMaintenance:  c.IcebergMaintenance,
		ArrowNative:         c.ArrowNative,
		MaxIdentifierLength: c.MaxIdentifierLength,
		IdentifierCase:      EngineCapabilitiesIdentifierCase(c.IdentifierCase),
	}
	if c.CancelScope != "" {
		scope := EngineCapabilitiesCancelScope(c.CancelScope)
		out.CancelScope = &scope
	}
	return out
}

// describeEngine is an engine row with its dialect and, when its adapter is
// available and the engine enabled, its capabilities and version.
func (h *Handlers) describeEngine(ctx context.Context, e db.Engine) Engine {
	out := engineToPublic(e)
	if acts := h.engineActions(ctx, e.ID); acts != nil {
		out.AllowedActions = &acts
	}
	if d, ok := dialect.ForEngineKind(e.Kind); ok {
		out.Dialect = dialectToPublic(d)
	}
	if h.engines == nil {
		return out
	}
	eng, err := h.engines.Get(ctx, e.ID)
	if err != nil {
		return out // disabled, or no adapter for the kind yet
	}
	out.Capabilities = capabilitiesToPublic(eng.Capabilities())
	out.Version = h.engineVersion(ctx, e, eng)
	return out
}

func (h *Handlers) engineVersion(ctx context.Context, e db.Engine, eng queryengine.Engine) *string {
	key := hex.EncodeToString(e.ID.Bytes[:]) + "@" + e.UpdatedAt.Time.String()
	if h.versionCache != nil {
		if v, ok := h.versionCache.Get(key); ok {
			return v
		}
	}
	var out *string
	if v, ok := queryengine.As[queryengine.Versioner](eng); ok {
		vctx, cancel := context.WithTimeout(ctx, engineVersionTimeout)
		version, err := v.Version(vctx)
		cancel()
		if err == nil && version != "" {
			out = &version
		}
	}
	if h.versionCache != nil {
		h.versionCache.Set(key, out)
	}
	return out
}

func dialectNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: "not_found", Message: "dialect not found"}
}

// dialectETag is a strong validator of the dialect's whole document.
func dialectETag(body Dialect) string {
	raw, _ := json.Marshal(body)
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// etagMatches implements If-None-Match: *, a list, and weak validators.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

func (h *Handlers) GetDialect(ctx context.Context, request GetDialectRequestObject) (GetDialectResponseObject, error) {
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return GetDialect401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	d, ok := dialect.Get(request.Name)
	if !ok {
		return GetDialect404JSONResponse{NotFoundJSONResponse: dialectNotFound()}, nil
	}
	body := Dialect{
		Name: d.Name(), FormatterLanguage: d.FormatterLanguage(), CodemirrorBase: d.CodeMirrorBase(),
		Keywords: d.Keywords(), Functions: d.Functions(),
	}
	etag := dialectETag(body)
	if request.Params.IfNoneMatch != nil && etagMatches(*request.Params.IfNoneMatch, etag) {
		return GetDialect304Response{Headers: GetDialect304ResponseHeaders{ETag: etag}}, nil
	}
	return GetDialect200JSONResponse{
		Body:    body,
		Headers: GetDialect200ResponseHeaders{ETag: etag, CacheControl: "private, max-age=3600"},
	}, nil
}

// SplitStatements answers statement offsets in UTF-16 code units, the
// indices of the editor's JavaScript strings.
func (h *Handlers) SplitStatements(ctx context.Context, request SplitStatementsRequestObject) (SplitStatementsResponseObject, error) {
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return SplitStatements401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	d, ok := dialect.Get(request.Name)
	if !ok {
		return SplitStatements404JSONResponse{NotFoundJSONResponse: dialectNotFound()}, nil
	}
	if request.Body == nil {
		return SplitStatements422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: "validation_error", Message: "sql is required"}}, nil
	}
	sql := request.Body.Sql
	if utf8.RuneCountInString(sql) > maxSplitSQL {
		return SplitStatements422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
			Code: "validation_error", Message: fmt.Sprintf("sql is longer than %d characters", maxSplitSQL),
		}}, nil
	}
	spans, err := d.Statements(sql)
	if err != nil {
		code := "invalid_sql"
		if errors.Is(err, dialect.ErrDelimiter) {
			code = "unsupported_syntax"
		}
		return SplitStatements422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: code, Message: err.Error()}}, nil
	}
	out := make([]StatementSpan, 0, len(spans))
	pos, units := 0, 0 // byte offset and the UTF-16 units before it
	advance := func(to int) int {
		for pos < to {
			r, size := utf8.DecodeRuneInString(sql[pos:])
			units += utf16.RuneLen(r)
			pos += size
		}
		return units
	}
	for _, s := range spans {
		start := advance(s.Start)
		out = append(out, StatementSpan{Start: start, End: advance(s.End)})
	}
	return SplitStatements200JSONResponse(out), nil
}
