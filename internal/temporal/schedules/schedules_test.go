package schedules

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// fakeClient is a client.Client whose only implemented surface is the schedule
// client.
type fakeClient struct {
	client.Client
	sc *fakeScheduleClient
}

func (f *fakeClient) ScheduleClient() client.ScheduleClient { return f.sc }

type fakeScheduleClient struct {
	client.ScheduleClient
	createErr   error
	createCalls int
	updateCalls int
	// created and updated record the spec of the last call.
	created client.ScheduleSpec
	updated *client.ScheduleSpec
}

func (f *fakeScheduleClient) Create(ctx context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
	f.created = options.Spec
	f.createCalls++
	return &fakeHandle{sc: f}, f.createErr
}

func (f *fakeScheduleClient) GetHandle(ctx context.Context, scheduleID string) client.ScheduleHandle {
	return &fakeHandle{sc: f}
}

type fakeHandle struct {
	client.ScheduleHandle
	sc *fakeScheduleClient
}

func (h *fakeHandle) GetID() string { return "" }

func (h *fakeHandle) Update(ctx context.Context, options client.ScheduleUpdateOptions) error {
	h.sc.updateCalls++
	update, err := options.DoUpdate(client.ScheduleUpdateInput{})
	if err == nil && update != nil && update.Schedule != nil {
		h.sc.updated = update.Schedule.Spec
	}
	return err
}

func testSpecs() []Spec {
	return []Spec{
		{ID: "a", Workflow: func() {}, Every: time.Second},
		{ID: "b", Workflow: func() {}, Every: 2 * time.Second},
	}
}

// TestEnsure_CreatesWhenAbsent pins that Ensure creates every schedule on first
// boot.
func TestEnsure_CreatesWhenAbsent(t *testing.T) {
	t.Parallel()
	sc := &fakeScheduleClient{}
	err := Ensure(context.Background(), &fakeClient{sc: sc}, "queue", testSpecs())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if sc.createCalls != 2 {
		t.Fatalf("create calls = %d, want 2", sc.createCalls)
	}
	if sc.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", sc.updateCalls)
	}
}

// TestEnsure_UpatesWhenExists pins idempotency across worker boots: a create
// collision is followed by an update, one schedule per ID.
func TestEnsure_UpatesWhenExists(t *testing.T) {
	t.Parallel()
	sc := &fakeScheduleClient{createErr: temporal.ErrScheduleAlreadyRunning}
	err := Ensure(context.Background(), &fakeClient{sc: sc}, "queue", testSpecs())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if sc.createCalls != 2 {
		t.Fatalf("create calls = %d, want 2 (one collision per spec)", sc.createCalls)
	}
	if sc.updateCalls != 2 {
		t.Fatalf("update calls = %d, want 2", sc.updateCalls)
	}
}

// TestEnsure_CalendarSpec pins the calendar form: a cron expression is sent as
// a Temporal cron in UTC and no interval, on create and on update alike, so a
// schedule that moves from an interval to a weekday is rewritten on the next boot.
func TestEnsure_CalendarSpec(t *testing.T) {
	t.Parallel()
	specs := []Spec{{ID: "weekly", Workflow: func() {}, Cron: "0 3 * * 0"}}

	sc := &fakeScheduleClient{}
	if err := Ensure(context.Background(), &fakeClient{sc: sc}, "queue", specs); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := sc.created.CronExpressions; len(got) != 1 || got[0] != "0 3 * * 0" {
		t.Fatalf("created cron = %v, want [0 3 * * 0]", got)
	}
	if len(sc.created.Intervals) != 0 || sc.created.TimeZoneName != "UTC" {
		t.Fatalf("created spec = %+v, want no interval and UTC", sc.created)
	}

	existing := &fakeScheduleClient{createErr: temporal.ErrScheduleAlreadyRunning}
	if err := Ensure(context.Background(), &fakeClient{sc: existing}, "queue", specs); err != nil {
		t.Fatalf("Ensure (update): %v", err)
	}
	if existing.updated == nil || len(existing.updated.CronExpressions) != 1 || len(existing.updated.Intervals) != 0 {
		t.Fatalf("updated spec = %+v, want the cron and no interval", existing.updated)
	}
}

// TestEnsure_RejectsBadSpecs: a spec needs exactly one way of saying when, and a
// cron that parses; the worker must fail at boot, never run a job that never fires.
func TestEnsure_RejectsBadSpecs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		spec Spec
	}{
		{"neither every nor cron", Spec{ID: "a", Workflow: func() {}}},
		{"both every and cron", Spec{ID: "a", Workflow: func() {}, Every: time.Hour, Cron: "0 3 * * 0"}},
		{"six fields", Spec{ID: "a", Workflow: func() {}, Cron: "0 0 3 * * 0"}},
		{"not a cron", Spec{ID: "a", Workflow: func() {}, Cron: "every sunday"}},
		{"out of range", Spec{ID: "a", Workflow: func() {}, Cron: "0 25 * * *"}},
		{"no id", Spec{Workflow: func() {}, Every: time.Hour}},
		{"no workflow", Spec{ID: "a", Every: time.Hour}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sc := &fakeScheduleClient{}
			if err := Ensure(context.Background(), &fakeClient{sc: sc}, "queue", []Spec{tc.spec}); err == nil {
				t.Fatal("Ensure accepted the spec")
			}
			if sc.createCalls != 0 {
				t.Fatalf("a rejected spec created %d schedules", sc.createCalls)
			}
		})
	}
}
