package queryengine_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
)

func TestRegisterKind_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a kind twice did not panic")
		}
	}()
	queryengine.RegisterKind(fake.Kind, queryengine.ClassDirect, nil)
}

func TestKinds_SortedAndRegistered(t *testing.T) {
	kinds := queryengine.Kinds()
	if !slices.IsSorted(kinds) {
		t.Fatalf("kinds = %v, want sorted", kinds)
	}
	for _, k := range []string{fake.Kind, readOnlyNoneKind} {
		if !slices.Contains(kinds, k) {
			t.Fatalf("kinds = %v, want %q", kinds, k)
		}
	}
}

func TestTransient(t *testing.T) {
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("unknown kind", func(t *testing.T) {
		if _, err := queryengine.Transient(context.Background(), "nope", connections.Config{}, d); !errors.Is(err, queryengine.ErrKindNotRegistered) {
			t.Fatalf("err = %v, want ErrKindNotRegistered", err)
		}
	})
	t.Run("registered kind builds an unregistered engine", func(t *testing.T) {
		e, err := queryengine.Transient(context.Background(), fake.Kind, connections.Config{}, d)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = e.Close() }()
		if err := e.Ping(context.Background()); err != nil {
			t.Fatalf("ping = %v", err)
		}
	})
}
