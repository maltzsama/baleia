package realtime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// StreamMessage is one Stream entry.
type StreamMessage struct {
	ID     string
	Values map[string]any
}

// StreamAdd appends values to stream and returns the entry id. maxLen > 0
// keeps the stream near that length (approximate trim).
func (c *Client) StreamAdd(ctx context.Context, stream string, values map[string]any, maxLen int64) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	args := &redis.XAddArgs{Stream: c.key("stream", stream), Values: values}
	if maxLen > 0 {
		args.MaxLen = maxLen
		args.Approx = true
	}
	return c.rdb.XAdd(opCtx, args).Result()
}

// StreamEnsureGroup creates the consumer group (and the stream) if it does not
// exist yet. An existing group is not an error.
func (c *Client) StreamEnsureGroup(ctx context.Context, stream, group string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	err := c.rdb.XGroupCreateMkStream(opCtx, c.key("stream", stream), c.key("group", group), "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// StreamReadGroup reads up to count new entries for consumer. block 0 returns
// immediately; a positive block waits that long for an entry. It returns nil
// (not an error) when there is nothing new.
func (c *Client) StreamReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]StreamMessage, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if count <= 0 {
		count = 32
	}
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.key("group", group),
		Consumer: consumer,
		Streams:  []string{c.key("stream", stream), ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []StreamMessage
	for _, s := range res {
		out = append(out, flattenMessages(s.Messages)...)
	}
	return out, nil
}

// StreamAck acknowledges delivered entries so they leave the pending list.
func (c *Client) StreamAck(ctx context.Context, stream, group string, ids ...string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	if len(ids) == 0 {
		return nil
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	return c.rdb.XAck(opCtx, c.key("stream", stream), c.key("group", group), ids...).Err()
}

// StreamClaimPending reclaims entries another consumer left pending for at
// least minIdle and delivers them to consumer. It is the recovery path for a
// relay that crashed before acknowledging.
func (c *Client) StreamClaimPending(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int64) ([]StreamMessage, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if count <= 0 {
		count = 32
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	msgs, _, err := c.rdb.XAutoClaim(opCtx, &redis.XAutoClaimArgs{
		Stream:   c.key("stream", stream),
		Group:    c.key("group", group),
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return flattenMessages(msgs), nil
}

// StreamTrim removes entries older than before, leaving the replay window
// intact. Acknowledgement alone never deletes an entry.
func (c *Client) StreamTrim(ctx context.Context, stream string, before time.Time) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	opCtx, cancel := c.operation(ctx)
	defer cancel()
	minID := strconv.FormatInt(before.UnixMilli(), 10) + "-0"
	return c.rdb.XTrimMinID(opCtx, c.key("stream", stream), minID).Err()
}

func flattenMessages(msgs []redis.XMessage) []StreamMessage {
	out := make([]StreamMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, StreamMessage{ID: m.ID, Values: m.Values})
	}
	return out
}
