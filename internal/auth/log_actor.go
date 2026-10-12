package auth

import "context"

// logActorKey holds a mutable slot for the request's authenticated actor. The
// request-logging middleware logs after the handler returns, so a handler that
// resolves the actor itself (the WebSocket token flow) can record it here for
// the middleware to pick up.
type logActorKey struct{}

// LogActorHolder is a mutable slot carried in the request context.
type LogActorHolder struct {
	ActorID string
}

// WithLogActorHolder attaches an actor holder to ctx.
func WithLogActorHolder(ctx context.Context) context.Context {
	return context.WithValue(ctx, logActorKey{}, &LogActorHolder{})
}

// SetLogActor records userID on the holder in ctx, if one is present.
func SetLogActor(ctx context.Context, userID string) {
	if h, ok := ctx.Value(logActorKey{}).(*LogActorHolder); ok {
		h.ActorID = userID
	}
}

// LogActor returns the actor recorded on the holder in ctx, or "".
func LogActor(ctx context.Context) string {
	if h, ok := ctx.Value(logActorKey{}).(*LogActorHolder); ok {
		return h.ActorID
	}
	return ""
}
