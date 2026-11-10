package realtime

import "context"

// Publish sends payload to every replica subscribed to channel. A disabled
// client drops the message (there is no shared layer).
func (c *Client) Publish(ctx context.Context, channel string, payload []byte) error {
	if !c.Enabled() {
		return nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return c.rdb.Publish(opCtx, c.key("bus", channel), payload).Err()
}

// Subscribe delivers every message on channel to deliver until ctx ends. It
// blocks, so callers run it in a goroutine. A disabled client returns
// ErrDisabled immediately so the caller can pick its local fallback.
func (c *Client) Subscribe(ctx context.Context, channel string, deliver func([]byte)) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	sub := c.rdb.Subscribe(ctx, c.key("bus", channel))
	defer func() { _ = sub.Close() }()
	// Wait for the subscription to be acknowledged so a message published
	// right after the caller starts this goroutine is not lost.
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-messages:
			if !ok {
				return nil
			}
			deliver([]byte(msg.Payload))
		}
	}
}

// SubscribeError is Subscribe with errors surfaced: it returns on any receive
// error (including a dropped connection) so the caller can reconnect and
// resync. onSubscribed, when set, runs after each successful subscription
// acknowledgement, which is when the caller should resync. Subscribe, by
// contrast, lets the driver reconnect silently.
func (c *Client) SubscribeError(ctx context.Context, channel string, onSubscribed func(), deliver func([]byte)) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	sub := c.rdb.Subscribe(ctx, c.key("bus", channel))
	defer func() { _ = sub.Close() }()
	// ReceiveMessage does not unblock on ctx cancellation; closing the
	// connection does.
	stop := context.AfterFunc(ctx, func() { _ = sub.Close() })
	defer stop()
	// Wait for the subscription to be acknowledged before reporting connected.
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	if onSubscribed != nil {
		onSubscribed()
	}
	for {
		msg, err := sub.ReceiveMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		deliver([]byte(msg.Payload))
	}
}
