package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/realtime"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// StreamName is the Redis Stream the producer appends to.
	StreamName = "webhooks"
	// GroupName is the consumer group the relay drains the Stream with.
	GroupName = "webhook-delivery"
	// maxAttempts caps the retries before a delivery is dead-lettered.
	maxAttempts = 8
	// baseBackoff is the first retry delay; it doubles per attempt.
	baseBackoff = 2 * time.Second
)

// DeliveryStore is the durable side of the relay, implemented by
// repo.Webhooks. The interface keeps this package free of the store import.
type DeliveryStore interface {
	Match(ctx context.Context, workspaceID, event string) ([]model.Webhook, error)
	MatchEvent(ctx context.Context, event string) ([]model.Webhook, error)
	EnqueueDelivery(ctx context.Context, d model.WebhookDelivery) (bool, error)
	DueDeliveries(ctx context.Context, limit int) ([]model.WebhookDelivery, error)
	ClaimDueDeliveries(ctx context.Context, limit int, lease time.Duration) ([]model.WebhookDelivery, error)
	MarkDelivered(ctx context.Context, id uint64) error
	MarkFailed(ctx context.Context, id uint64, attempts int, next *time.Time, lastError string) error
}

// Relay drains the webhooks Stream into durable delivery rows and delivers
// them with an HMAC signature, retry/backoff, a dead-letter state and a metric.
type Relay struct {
	logger   *slog.Logger
	client   *realtime.Client
	store    DeliveryStore
	http     *http.Client
	keyring  *crypto.Keyring
	consumer string
	outcome  *prometheus.CounterVec
}

// NewRelay builds the delivery relay. consumer must be unique per process; reg
// may be nil (no metric).
func NewRelay(logger *slog.Logger, client *realtime.Client, store DeliveryStore, httpClient *http.Client, keyring *crypto.Keyring, consumer string, reg *prometheus.Registry) *Relay {
	r := &Relay{logger: logger, client: client, store: store, http: httpClient, keyring: keyring, consumer: consumer}
	if reg != nil {
		r.outcome = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "baleia_webhook_deliveries_total",
			Help: "Webhook delivery attempts by outcome (delivered, failed, dead).",
		}, []string{"outcome"})
		reg.MustRegister(r.outcome)
	}
	return r
}

// Run drains the stream and delivers due rows until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	if r.client == nil || !r.client.Enabled() {
		return
	}
	for ctx.Err() == nil {
		if err := r.client.StreamEnsureGroup(ctx, StreamName, GroupName); err != nil {
			r.warn(ctx, "ensure consumer group", err)
			sleep(ctx, time.Second)
			continue
		}
		// Reclaim entries a crashed consumer left pending, then read new ones.
		if pending, err := r.client.StreamClaimPending(ctx, StreamName, GroupName, r.consumer, time.Minute, 32); err == nil {
			r.ingest(ctx, pending)
		}
		if msgs, err := r.client.StreamReadGroup(ctx, StreamName, GroupName, r.consumer, 32, 2*time.Second); err == nil {
			r.ingest(ctx, msgs)
		} else if ctx.Err() == nil {
			r.warn(ctx, "read stream", err)
			sleep(ctx, time.Second)
			continue
		}
		r.deliverDue(ctx)
	}
}

// ingest records a delivery row per matching webhook, then acknowledges the
// stream entry. The delivery rows are the durable retry/replay record.
func (r *Relay) ingest(ctx context.Context, msgs []realtime.StreamMessage) {
	for _, m := range msgs {
		raw, _ := m.Values["event"].(string)
		var ev Event
		if json.Unmarshal([]byte(raw), &ev) != nil || ev.ID == "" {
			_ = r.client.StreamAck(ctx, StreamName, GroupName, m.ID)
			continue
		}
		var hooks []model.Webhook
		var err error
		acked := true
		if ev.WorkspaceID == "" {
			hooks, err = r.store.MatchEvent(ctx, ev.Type)
		} else {
			hooks, err = r.store.Match(ctx, ev.WorkspaceID, ev.Type)
		}
		if err != nil {
			r.warn(ctx, "match webhooks", err)
			continue
		}
		for _, h := range hooks {
			if _, err := r.store.EnqueueDelivery(ctx, model.WebhookDelivery{
				WebhookID: h.ID,
				EventID:   ev.ID,
				Event:     ev.Type,
				Payload:   model.JSON(raw),
				Status:    "pending",
			}); err != nil {
				// Keep the stream entry pending so the event is retried.
				r.warn(ctx, "enqueue delivery", err)
				acked = false
			}
		}
		if acked {
			_ = r.client.StreamAck(ctx, StreamName, GroupName, m.ID)
		}
	}
}

// deliverDue retries pending rows and dead-letters the ones out of attempts.
func (r *Relay) deliverDue(ctx context.Context) {
	rows, err := r.store.ClaimDueDeliveries(ctx, 32, 2*time.Minute)
	if err != nil {
		r.warn(ctx, "due deliveries", err)
		return
	}
	now := time.Now().UTC()
	for _, d := range rows {
		if d.Webhook == nil {
			_ = r.store.MarkFailed(ctx, d.ID, d.Attempts+1, nil, "webhook removed")
			r.count("dead")
			continue
		}
		attempts := d.Attempts + 1
		err := r.post(ctx, *d.Webhook, d)
		if err == nil {
			r.count("delivered")
			_ = r.store.MarkDelivered(ctx, d.ID)
			continue
		}
		r.count("failed")
		if attempts >= maxAttempts {
			r.count("dead")
			_ = r.store.MarkFailed(ctx, d.ID, attempts, nil, err.Error())
			continue
		}
		next := now.Add(baseBackoff * time.Duration(1<<uint(attempts-1)))
		_ = r.store.MarkFailed(ctx, d.ID, attempts, &next, err.Error())
	}
}

// post signs and sends one delivery. The signature is
// "sha256=<hex HMAC-SHA256(secret, body)>".
func (r *Relay) post(ctx context.Context, h model.Webhook, d model.WebhookDelivery) error {
	secret, err := r.keyring.Open(h.SecretEnc, "webhook:"+string(h.ID))
	if err != nil {
		return fmt.Errorf("open webhook secret: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(d.Payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Baleia-Event", d.Event)
	req.Header.Set("X-Baleia-Delivery", strconv.FormatUint(d.ID, 10))
	req.Header.Set("X-Baleia-Signature", Sign(secret, d.Payload))

	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func (r *Relay) count(outcome string) {
	if r.outcome != nil {
		r.outcome.WithLabelValues(outcome).Inc()
	}
}

func (r *Relay) warn(ctx context.Context, msg string, err error) {
	if r.logger != nil {
		r.logger.WarnContext(ctx, "webhook relay: "+msg, "err", err)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// Sign returns the delivery signature for a body.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ErrDisabled reports that the relay cannot run without the shared layer.
var ErrDisabled = errors.New("webhooks: relay needs BALEIA_REDIS_URL")
