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
}

func (f *fakeScheduleClient) Create(ctx context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
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
	_, err := options.DoUpdate(client.ScheduleUpdateInput{})
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
