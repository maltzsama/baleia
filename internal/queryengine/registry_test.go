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

func fakeSpec(name string) queryengine.KindSpec {
	return queryengine.KindSpec{Name: name, Class: queryengine.ClassDirect, Factory: func(_ context.Context, row queryengine.EngineRow, _ connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		return fake.New(fake.Options{ID: row.ID, Dialer: d}), nil
	}}
}

func TestNewRegistry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		specs   []queryengine.KindSpec
		wantErr bool
	}{
		{name: "empty", specs: nil},
		{name: "two kinds", specs: []queryengine.KindSpec{fakeSpec("a"), fakeSpec("b")}},
		{name: "duplicate kind", specs: []queryengine.KindSpec{fakeSpec("a"), fakeSpec("a")}, wantErr: true},
		{name: "no name", specs: []queryengine.KindSpec{fakeSpec("")}, wantErr: true},
		{name: "no factory", specs: []queryengine.KindSpec{{Name: "a"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := queryengine.NewRegistry(tt.specs...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistry_HasAndKinds(t *testing.T) {
	t.Parallel()
	r, err := queryengine.NewRegistry(fakeSpec("b"), fakeSpec("a"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Kinds(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("kinds = %v, want [a b]", got)
	}
	if !r.Has("a") || r.Has("c") {
		t.Fatalf("Has(a) = %v, Has(c) = %v", r.Has("a"), r.Has("c"))
	}
}

func TestRegistry_Transient(t *testing.T) {
	t.Parallel()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := queryengine.NewRegistry(fakeSpec("a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("unknown kind", func(t *testing.T) {
		t.Parallel()
		if _, err := r.Transient(context.Background(), "nope", connections.Config{}, d); !errors.Is(err, queryengine.ErrKindNotRegistered) {
			t.Fatalf("err = %v, want ErrKindNotRegistered", err)
		}
	})
	t.Run("registered kind", func(t *testing.T) {
		t.Parallel()
		e, err := r.Transient(context.Background(), "a", connections.Config{}, d)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = e.Close() }()
		if err := e.Ping(context.Background()); err != nil {
			t.Fatalf("ping = %v", err)
		}
	})
}
