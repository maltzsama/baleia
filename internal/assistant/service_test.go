package assistant

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/crypto"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/galileostudio/baleia/internal/testdb"
)

func strptr(v string) *string { return &v }

// TestSettingsKeyIsWriteOnly: the provider key is sealed at rest and opened only
// by the adapters; the stored row never carries the plaintext.
func TestSettingsKeyIsWriteOnly(t *testing.T) {
	pool := testdb.New(t)
	keyring, err := crypto.NewKeyring("0123456789abcdef0123456789abcdef", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(adb.New(pool), keyring)
	secret := "sk-test-secret"

	row, err := svc.UpdateSettings(context.Background(), SettingsInput{
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
	row, err = svc.UpdateSettings(context.Background(), SettingsInput{
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
