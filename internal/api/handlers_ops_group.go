package api

import (
	"context"
	"errors"
	"sync"

	"github.com/galileostudio/baleia/internal/audit/trail"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// OpsHandlers serves monitoring, system status, attention items and the audit log (#365).
type OpsHandlers struct {
	*core
	// schema reports the applied migration version (poolSchema outside tests).
	schema schemaVersioner
	// schedules and engineStatus are the status page's probes (#291); tests
	// give fakes so it runs without a Temporal server or an engine. A nil
	// schedules means there is no Temporal client.
	schedules    scheduleDescriber
	engineStatus engineStatusProber
	// attentionCache caches the computed attention items for 30 s per user (#261).
	attentionCache *TTLStore[[]AttentionItem]
	attentionOnce  sync.Once
	// monitor reads engine and table snapshots and records maintenance runs
	// (#117).
	monitor *monitoring.Service
	// trail reads and exports the audit trail (#43).
	trail *trail.Service
}

// scheduleDescriber reports whether a Temporal schedule exists; detail is a
// stable code, empty when the schedule is fine.
type scheduleDescriber interface {
	DescribeSchedule(ctx context.Context, id string) (status SystemStatusItemStatus, detail string)
}

// temporalSchedules asks the Temporal server.
type temporalSchedules struct{ client client.Client }

func (t temporalSchedules) DescribeSchedule(ctx context.Context, id string) (SystemStatusItemStatus, string) {
	_, err := t.client.ScheduleClient().GetHandle(ctx, id).Describe(ctx)
	if err == nil {
		return SystemStatusItemStatusOk, ""
	}
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return SystemStatusItemStatusMissing, "schedule_missing"
	}
	return SystemStatusItemStatusUnknown, "schedule_unknown"
}

// engineStatusProber reports an engine's health; detail (its version when
// known) replaces the kind shown by default when it is not empty.
type engineStatusProber interface {
	ProbeEngine(ctx context.Context, e db.Engine) (status SystemStatusItemStatus, detail string)
}

// managedEngines probes through the engine manager and the shared probe cache.
type managedEngines struct{ c *core }

func (m managedEngines) ProbeEngine(ctx context.Context, e db.Engine) (SystemStatusItemStatus, string) {
	if m.c.engines == nil {
		return SystemStatusItemStatusUnknown, ""
	}
	eng, err := m.c.engines.Get(ctx, e.ID)
	if err != nil {
		return SystemStatusItemStatusUnknown, ""
	}
	probe := m.c.engineProbeFor(ctx, e, eng)
	detail := ""
	if probe.Version != nil {
		detail = *probe.Version
	}
	return SystemStatusItemStatus(probe.Health.Status), detail
}

func newOpsHandlers(c *core, schema schemaVersioner) *OpsHandlers {
	o := &OpsHandlers{core: c, schema: schema, engineStatus: managedEngines{c: c}, monitor: monitoring.New(c.queries), trail: trail.New(c.queries)}
	if c.temporalClient != nil {
		o.schedules = temporalSchedules{client: c.temporalClient}
	}
	return o
}
