package wsticket

import (
	"testing"
	"time"
)

func TestTicketSingleUse(t *testing.T) {
	store := New()
	token, expires := store.Issue("user-1", "exec-1")
	if token == "" {
		t.Fatal("empty ticket")
	}
	if !expires.After(time.Now()) {
		t.Fatalf("ticket already expired: %s", expires)
	}

	ticket, ok := store.Consume(token)
	if !ok {
		t.Fatal("a fresh ticket must be accepted")
	}
	if ticket.UserID != "user-1" || ticket.ExecutionID != "exec-1" {
		t.Fatalf("unexpected ticket: %+v", ticket)
	}

	if _, ok := store.Consume(token); ok {
		t.Fatal("a ticket is valid exactly once")
	}
}

func TestTicketExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewWithClock(func() time.Time { return now })

	token, expires := store.Issue("user-1", "")
	if !expires.Equal(now.Add(TTL)) {
		t.Fatalf("expires %s, want %s", expires, now.Add(TTL))
	}

	now = now.Add(TTL + time.Second)
	if _, ok := store.Consume(token); ok {
		t.Fatal("an expired ticket must not be accepted")
	}
}
