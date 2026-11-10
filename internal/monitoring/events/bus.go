package events

import (
	"context"

	"github.com/galileostudio/baleia/internal/realtime"
)

// Bus is the cross-process Transport over the shared Redis pub/sub layer
// (ADR 0022). Every writer publishes and every API replica subscribes, so a
// change written by the worker reaches the SSE clients on any replica.
type Bus struct{ client *realtime.Client }

// NewBus builds the transport over the shared client.
func NewBus(client *realtime.Client) *Bus { return &Bus{client: client} }

// Notify publishes one change.
func (b *Bus) Notify(ctx context.Context, channel, payload string) error {
	return b.client.Publish(ctx, channel, []byte(payload))
}

// Listen relays every change on channel until ctx ends. It reports connected
// before subscribing; a dropped connection returns so the caller reconnects
// and resyncs the subscribers.
func (b *Bus) Listen(ctx context.Context, channel string, onConnected func(), deliver func(payload string)) error {
	if onConnected != nil {
		onConnected()
	}
	return b.client.SubscribeError(ctx, channel, func(payload []byte) {
		deliver(string(payload))
	})
}
