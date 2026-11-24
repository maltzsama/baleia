package datapolicy

import "time"

// Health is an engine's data policy integration as Baleia shows it (ADR
// 0028): disabled, enabled and healthy, or enabled and unhealthy. It comes
// from configuration and status reports, never from a single failed call.
type Health string

const (
	HealthDisabled  Health = "disabled"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// Reasons an enabled engine is unhealthy.
const (
	UnhealthyNoReport        = "no_report"
	UnhealthyReportStale     = "report_stale"
	UnhealthyBundleError     = "bundle_error"
	UnhealthyNoRevision      = "no_active_revision"
	UnhealthyUnknownRevision = "unknown_revision"
	UnhealthyLeaseExpired    = "lease_expired"
	UnhealthyDigestMismatch  = "data_policy_digest_mismatch"
)

// EngineState is one engine's integration: its health, why it is unhealthy,
// and the revision it runs next to the published one.
type EngineState struct {
	Health         Health
	Reason         string
	ActiveRevision string
	ReportedAt     *time.Time
}

// HealthInput is what an engine's health is judged on.
type HealthInput struct {
	// DeploymentEnabled is BALEIA_DATA_POLICIES=opa; EngineEnabled is the
	// engine's data_policies flag.
	DeploymentEnabled bool
	EngineEnabled     bool
	// Status is the engine's latest report, nil when it never reported.
	Status *EngineStatus
	// ActiveNotAfter is the lease end of the revision the engine reports
	// active, nil when Baleia stored no such revision: its lease cannot be
	// checked, so the engine is unhealthy. Past it, OPA denies every user.
	ActiveNotAfter *time.Time
	// ExpectedDigest is the digest of the Rego the engine must run.
	ExpectedDigest string
	// StaleAfter is how long a report stays current.
	StaleAfter time.Duration
	Now        time.Time
}

// EngineHealth judges one engine.
func EngineHealth(in HealthInput) EngineState {
	if !in.DeploymentEnabled || !in.EngineEnabled {
		return EngineState{Health: HealthDisabled}
	}
	if in.Status == nil {
		return EngineState{Health: HealthUnhealthy, Reason: UnhealthyNoReport}
	}
	reported := in.Status.ReportedAt
	state := EngineState{Health: HealthHealthy, ActiveRevision: in.Status.ActiveRevision, ReportedAt: &reported}
	switch {
	case in.Now.Sub(reported) > in.StaleAfter:
		state.Health, state.Reason = HealthUnhealthy, UnhealthyReportStale
	case in.Status.Code != "":
		state.Health, state.Reason = HealthUnhealthy, UnhealthyBundleError
	case in.Status.ActiveRevision == "":
		state.Health, state.Reason = HealthUnhealthy, UnhealthyNoRevision
	case in.ActiveNotAfter == nil:
		state.Health, state.Reason = HealthUnhealthy, UnhealthyUnknownRevision
	case !in.Now.Before(*in.ActiveNotAfter):
		state.Health, state.Reason = HealthUnhealthy, UnhealthyLeaseExpired
	case in.ExpectedDigest != "" && in.Status.PolicyDigest != in.ExpectedDigest:
		state.Health, state.Reason = HealthUnhealthy, UnhealthyDigestMismatch
	}
	return state
}
