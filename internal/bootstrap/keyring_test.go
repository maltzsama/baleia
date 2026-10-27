package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/config"
)

const testKey = "k-0123456789abcdef0123456789abcdef"

func counter(n int64, err error) CredentialCounter {
	return func(context.Context) (int64, error) { return n, err }
}

func TestLoadKeyring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("key configured", func(t *testing.T) {
		t.Parallel()
		k, err := LoadKeyring(ctx, &config.Config{EncryptionKey: testKey}, counter(5, nil))
		if err != nil || k == nil {
			t.Fatalf("LoadKeyring = %v, %v", k, err)
		}
	})

	t.Run("no key on a fresh install warns and starts", func(t *testing.T) {
		t.Parallel()
		k, err := LoadKeyring(ctx, &config.Config{}, counter(0, nil))
		if err != nil || k != nil {
			t.Fatalf("LoadKeyring = %v, %v; want nil, nil", k, err)
		}
	})

	t.Run("no key with stored credentials refuses to start", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadKeyring(ctx, &config.Config{}, counter(2, nil)); !errors.Is(err, ErrEncryptionKeyRequired) {
			t.Fatalf("want ErrEncryptionKeyRequired, got %v", err)
		}
	})

	t.Run("count failure is not swallowed", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadKeyring(ctx, &config.Config{}, counter(0, errors.New("db down"))); err == nil {
			t.Fatal("LoadKeyring ignored the count error")
		}
	})

	t.Run("weak key is rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadKeyring(ctx, &config.Config{EncryptionKey: "short"}, counter(0, nil)); err == nil {
			t.Fatal("LoadKeyring accepted a short key")
		}
	})
}
