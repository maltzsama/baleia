package assistant_test

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

func strptr(v string) *string { return &v }

// newAssistantService builds the assistant service over the GORM store, as the
// API does (ADR 0020).
func newAssistantService(t *testing.T, gdb *gorm.DB, keyring *crypto.Keyring) *assistant.Service {
	t.Helper()
	return assistant.New(repo.New(gdb), keyring)
}

// TestSettingsKeyIsWriteOnly: the provider key is sealed at rest and opened only
// by the adapters; the stored row never carries the plaintext.
func TestSettingsKeyIsWriteOnly(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	keyring, err := crypto.NewKeyring("0123456789abcdef0123456789abcdef", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := newAssistantService(t, pool, keyring)
	secret := "sk-test-secret"

	row, err := svc.UpdateSettings(context.Background(), assistant.SettingsInput{
		Enabled:                 true,
		Provider:                strptr("anthropic"),
		Model:                   strptr("claude-test"),
		APIKey:                  &secret,
		DataSharing:             "metadata_only",
		MaxTurnSeconds:          180,
		MaxToolCallsPerTurn:     10,
		MaxOutputTokensPerTurn:  4096,
		DailyTokenBudgetPerUser: 500000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ApiKeyEnc == nil || *row.ApiKeyEnc == secret {
		t.Fatalf("api_key_enc = %v, want a sealed value", row.ApiKeyEnc)
	}
	got, err := svc.APIKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Fatalf("APIKey() = %q, want the original", got)
	}

	// An empty update keeps the stored key.
	row, err = svc.UpdateSettings(context.Background(), assistant.SettingsInput{
		Enabled:                 true,
		Provider:                strptr("anthropic"),
		Model:                   strptr("claude-test"),
		DataSharing:             "metadata_only",
		MaxTurnSeconds:          180,
		MaxToolCallsPerTurn:     10,
		MaxOutputTokensPerTurn:  4096,
		DailyTokenBudgetPerUser: 500000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ApiKeyEnc == nil {
		t.Fatal("an omitted key cleared api_key_enc")
	}
}
