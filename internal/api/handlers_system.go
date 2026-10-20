package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/version"
	"go.temporal.io/sdk/client"
)

// systemStatusSectionTimeout bounds each dependency probe (#260). Baleia and
// SMTP are read from memory and do not need a timeout.
var systemStatusSectionTimeout = 2 * time.Second

// processStartedAt is the API process start time reported in the baleia
// section. Initialisation time is close enough to process start.
var processStartedAt = time.Now().UTC()

// GetSystemStatus — GET /admin/system/status (#260). Admins only. Each
// dependency is probed independently: a slow or broken section becomes
// `unknown` with a stable code, and the response is still 200.
func (h *Handlers) GetSystemStatus(ctx context.Context, _ GetSystemStatusRequestObject) (GetSystemStatusResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetSystemStatus401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if err := p.RequireAdmin(); err != nil {
		return GetSystemStatus403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "administrator role required"}}, nil
	}

	status := SystemStatus{
		Baleia: baleiaStatusSection(),
		Smtp:   smtpStatusSection(h.cfg),
	}
	h.probeDependencies(ctx, &status)
	h.probePlatform(ctx, &status)
	return GetSystemStatus200JSONResponse(status), nil
}

// probeDependencies fills the three network-backed sections concurrently so a
// slow dependency costs one section timeout, not the sum of them (#260).
func (h *Handlers) probeDependencies(ctx context.Context, status *SystemStatus) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); status.Database = h.databaseStatusSection(ctx) }()
	go func() { defer wg.Done(); status.Temporal = h.temporalStatusSection(ctx) }()
	go func() { defer wg.Done(); status.ObjectStorage = h.objectStorageStatusSection(ctx) }()
	wg.Wait()
}

func baleiaStatusSection() SystemStatusSection {
	latency := 0
	startedAt := processStartedAt
	v, commit := version.Version, version.Commit
	return SystemStatusSection{
		Status:    Ok,
		LatencyMs: &latency,
		Version:   &v,
		Commit:    &commit,
		StartedAt: &startedAt,
	}
}

func (h *Handlers) databaseStatusSection(ctx context.Context) SystemStatusSection {
	var migrationVersion int64
	section := runStatusSection(ctx, "database_unavailable", func(ctx context.Context) error {
		if h.pool == nil {
			return errors.New("no database pool")
		}
		return h.pool.QueryRow(ctx,
			`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&migrationVersion)
	})
	if section.Status == Ok {
		section.MigrationVersion = &migrationVersion
	}
	return section
}

func (h *Handlers) temporalStatusSection(ctx context.Context) SystemStatusSection {
	return runStatusSection(ctx, "temporal_unavailable", func(ctx context.Context) error {
		if h.temporalClient == nil {
			return errors.New("no temporal client")
		}
		_, err := h.temporalClient.CheckHealth(ctx, &client.CheckHealthRequest{})
		return err
	})
}

func (h *Handlers) objectStorageStatusSection(ctx context.Context) SystemStatusSection {
	return runStatusSection(ctx, "object_storage_unavailable", func(ctx context.Context) error {
		if h.s3 == nil {
			return errors.New("no object storage client")
		}
		return h.s3.Health(ctx)
	})
}

func smtpStatusSection(cfg *config.Config) SystemStatusSection {
	configured := cfg != nil && cfg.SMTPHost != "" && cfg.SMTPFrom != ""
	latency := 0
	section := SystemStatusSection{Status: Ok, LatencyMs: &latency, Configured: &configured}
	if !configured {
		code := "smtp_not_configured"
		section.Status = Unknown
		section.ErrorCode = &code
	}
	return section
}

// runStatusSection runs one probe under its own timeout. The raw error is
// discarded; only the stable code is exposed (#260).
func runStatusSection(ctx context.Context, errorCode string, probe func(context.Context) error) SystemStatusSection {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, systemStatusSectionTimeout)
	defer cancel()

	err := probe(ctx)
	latency := int(time.Since(start).Milliseconds())
	section := SystemStatusSection{Status: Ok, LatencyMs: &latency}
	if err != nil {
		section.Status = Unknown
		section.ErrorCode = &errorCode
	}
	return section
}
