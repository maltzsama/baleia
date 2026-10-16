package flightsql

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/queryengine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mapError turns a gRPC failure into a typed engine error. Messages are never
// parsed: the code is the whole signal.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return queryengine.NewError(queryengine.CodeCancelled, Kind, "", "statement cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return queryengine.NewError(queryengine.CodeTimeout, Kind, "", "statement timed out")
	}
	st, ok := status.FromError(err)
	if !ok {
		return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
	}
	native := st.Code().String()
	switch st.Code() {
	case codes.InvalidArgument:
		return queryengine.NewError(queryengine.CodeSyntax, Kind, native, st.Message())
	case codes.NotFound, codes.OutOfRange:
		return queryengine.NewError(queryengine.CodeNotFound, Kind, native, st.Message())
	case codes.PermissionDenied, codes.Unauthenticated:
		return queryengine.NewError(queryengine.CodePermission, Kind, native, st.Message())
	case codes.DeadlineExceeded:
		return queryengine.NewError(queryengine.CodeTimeout, Kind, native, st.Message())
	case codes.Canceled:
		return queryengine.NewError(queryengine.CodeCancelled, Kind, native, st.Message())
	case codes.ResourceExhausted:
		return queryengine.NewError(queryengine.CodeResource, Kind, native, st.Message())
	case codes.Unavailable:
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, native, st.Message())
	}
	return queryengine.NewError(queryengine.CodeInternal, Kind, native, st.Message())
}
