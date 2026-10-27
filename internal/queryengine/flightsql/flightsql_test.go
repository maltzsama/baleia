package flightsql

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSpec(t *testing.T) {
	t.Parallel()
	if s := Spec(dialect.NewRegistry().MustGet(dialect.FlightSQL)); s.Name != Kind || s.Factory == nil {
		t.Fatalf("spec = %+v, want kind %q with a factory", s, Kind)
	}
}

func TestMapError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code codes.Code
		want queryengine.ErrorCode
	}{
		{codes.InvalidArgument, queryengine.CodeSyntax},
		{codes.NotFound, queryengine.CodeNotFound},
		{codes.PermissionDenied, queryengine.CodePermission},
		{codes.Unauthenticated, queryengine.CodePermission},
		{codes.DeadlineExceeded, queryengine.CodeTimeout},
		{codes.Canceled, queryengine.CodeCancelled},
		{codes.ResourceExhausted, queryengine.CodeResource},
		{codes.Unavailable, queryengine.CodeUnavailable},
		{codes.Internal, queryengine.CodeInternal},
	}
	for _, tc := range cases {
		if got := queryengine.CodeOf(mapError(status.Error(tc.code, "x"))); got != tc.want {
			t.Errorf("%s -> %s, want %s", tc.code, got, tc.want)
		}
	}
	if got := queryengine.CodeOf(mapError(context.Canceled)); got != queryengine.CodeCancelled {
		t.Errorf("context.Canceled -> %s", got)
	}
	if got := queryengine.CodeOf(mapError(context.DeadlineExceeded)); got != queryengine.CodeTimeout {
		t.Errorf("context.DeadlineExceeded -> %s", got)
	}
}

func TestParseSetShow(t *testing.T) {
	t.Parallel()
	if n, v, ok := parseSet("SET x = '1'"); !ok || n != "x" || v != "1" {
		t.Errorf("parseSet = %q, %q, %v", n, v, ok)
	}
	if n, v, ok := parseSet("set Baleia_State = on"); !ok || n != "baleia_state" || v != "on" {
		t.Errorf("parseSet = %q, %q, %v", n, v, ok)
	}
	if _, _, ok := parseSet("SELECT 1"); ok {
		t.Error("SELECT parsed as SET")
	}
	if n, ok := parseShow("show x"); !ok || n != "x" {
		t.Errorf("parseShow = %q, %v", n, ok)
	}
	if _, ok := parseShow("SELECT 1"); ok {
		t.Error("SELECT parsed as SHOW")
	}
}
