package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/google/uuid"
)

func TestSystemStatus_AdminOnly(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{settings: SettingsFromConfig(&config.Config{}), schema: unreachableSchema{}})

	resp, err := h.GetSystemStatus(context.Background(), GetSystemStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(GetSystemStatus401JSONResponse); !ok {
		t.Fatalf("anonymous = %T, want 401", resp)
	}

	resp, err = h.GetSystemStatus(ctxAs(uuid.NewString(), "member"), GetSystemStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(GetSystemStatus403JSONResponse); !ok {
		t.Fatalf("member = %T, want 403", resp)
	}

	resp, err = h.GetSystemStatus(ctxAs(uuid.NewString(), "admin"), GetSystemStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(GetSystemStatus200JSONResponse)
	if !found {
		t.Fatalf("admin = %T, want 200", resp)
	}
	// Dependencies are not configured in this fixture, but the call still
	// succeeds and isolates them as unknown.
	if ok.Database.Status != Unknown || ok.Temporal.Status != Unknown || ok.ObjectStorage.Status != Unknown {
		t.Fatalf("sections = %+v, want unknown without deps", ok)
	}
	if ok.Database.ErrorCode == nil || *ok.Database.ErrorCode != "database_unavailable" {
		t.Fatalf("database error code = %v", ok.Database.ErrorCode)
	}
}

func TestSystemStatus_SectionTimeout(t *testing.T) {
	t.Parallel()
	section := runStatusSection(context.Background(), 20*time.Millisecond, "database_unavailable", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if section.Status != Unknown {
		t.Fatalf("status = %q, want unknown", section.Status)
	}
	if section.ErrorCode == nil || *section.ErrorCode != "database_unavailable" {
		t.Fatalf("error code = %v", section.ErrorCode)
	}
}

func TestSystemStatus_NoSecrets(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{settings: SettingsFromConfig(&config.Config{
		SMTPHost:     "smtp.internal.example",
		SMTPFrom:     "ops@internal.example",
		SMTPUser:     "smtp-service",
		SMTPPassword: "smtp-secret",
		S3Endpoint:   "s3.internal.example:9000",
		TemporalHost: "temporal.internal:7233",
	}), schema: unreachableSchema{}})

	resp, err := h.GetSystemStatus(ctxAs(uuid.NewString(), "admin"), GetSystemStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(GetSystemStatus200JSONResponse)
	if !found {
		t.Fatalf("response = %T, want 200", resp)
	}
	if ok.Smtp.Configured == nil || !*ok.Smtp.Configured || ok.Smtp.Status != Ok {
		t.Fatalf("smtp section = %+v, want configured/ok", ok.Smtp)
	}

	raw, err := json.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, secret := range []string{
		"smtp.internal.example",
		"ops@internal.example",
		"smtp-service",
		"smtp-secret",
		"s3.internal.example",
		"temporal.internal",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaks %q: %s", secret, body)
		}
	}
}

func TestSystemStatus_SMTPNotConfigured(t *testing.T) {
	t.Parallel()
	section := smtpStatusSection(MailSettings{})
	if section.Status != Unknown || section.Configured == nil || *section.Configured {
		t.Fatalf("smtp section = %+v, want unconfigured/unknown", section)
	}
	if section.ErrorCode == nil || *section.ErrorCode != "smtp_not_configured" {
		t.Fatalf("error code = %v", section.ErrorCode)
	}
}

// TestSystemStatus_DatabaseMigrationVersion probes a real migrated database.
func TestSystemStatus_DatabaseMigrationVersion(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t)
	resp, err := f.h.GetSystemStatus(ctxAs(f.admin, "admin"), GetSystemStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(GetSystemStatus200JSONResponse)
	if !found {
		t.Fatalf("response = %T, want 200", resp)
	}
	if ok.Database.Status != Ok {
		t.Fatalf("database = %+v", ok.Database)
	}
	if ok.Database.MigrationVersion == nil || *ok.Database.MigrationVersion <= 0 {
		t.Fatalf("migration version = %v", ok.Database.MigrationVersion)
	}
}

// unreachableSchema is a database that cannot be reached.
type unreachableSchema struct{}

func (unreachableSchema) SchemaVersion(context.Context) (int64, error) {
	return 0, errors.New("database unreachable")
}
