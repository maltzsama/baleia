package datapolicy

import (
	"testing"
	"time"
)

func TestEngineHealth(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	fresh := &EngineStatus{ReportedAt: now.Add(-time.Minute), ActiveRevision: "r1", PolicyDigest: "d1"}
	tests := []struct {
		name   string
		input  HealthInput
		health Health
		reason string
	}{
		{name: "deployment off", input: HealthInput{EngineEnabled: true, Status: fresh}, health: HealthDisabled},
		{name: "engine off", input: HealthInput{DeploymentEnabled: true, Status: fresh}, health: HealthDisabled},
		{name: "never reported", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true}, health: HealthUnhealthy, reason: UnhealthyNoReport},
		{name: "healthy", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: fresh, ExpectedDigest: "d1"}, health: HealthHealthy},
		{name: "stale report", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: &EngineStatus{ReportedAt: now.Add(-time.Hour)}}, health: HealthUnhealthy, reason: UnhealthyReportStale},
		{name: "activation error", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: &EngineStatus{ReportedAt: now, Code: "bundle_error"}}, health: HealthUnhealthy, reason: UnhealthyBundleError},
		{name: "no data revision", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: &EngineStatus{ReportedAt: now, PolicyDigest: "d1"}, ExpectedDigest: "d1"}, health: HealthUnhealthy, reason: UnhealthyNoRevision},
		{name: "lease passed", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: fresh, ExpectedDigest: "d1", ActiveNotAfter: new(now.Add(-time.Second))}, health: HealthUnhealthy, reason: UnhealthyLeaseExpired},
		{name: "lease running", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: fresh, ExpectedDigest: "d1", ActiveNotAfter: new(now.Add(time.Hour))}, health: HealthHealthy},
		{name: "another Rego", input: HealthInput{DeploymentEnabled: true, EngineEnabled: true, Status: fresh, ExpectedDigest: "d2"}, health: HealthUnhealthy, reason: UnhealthyDigestMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := tt.input
			in.Now, in.StaleAfter = now, 10*time.Minute
			got := EngineHealth(in)
			if got.Health != tt.health || got.Reason != tt.reason {
				t.Fatalf("EngineHealth = %s %q, want %s %q", got.Health, got.Reason, tt.health, tt.reason)
			}
		})
	}
}
