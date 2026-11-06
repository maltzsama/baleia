package events

import "testing"

func TestParsePayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		engine string
		kind   Kind
		ok     bool
	}{
		{"abc:queries", "abc", KindQueries, true},
		{"abc:cluster", "abc", KindCluster, true},
		{"abc:other", "", "", false},
		{"abc", "", "", false},
		{":queries", "", "", false},
	}
	for _, c := range cases {
		engine, kind, ok := ParsePayload(c.in)
		if engine != c.engine || kind != c.kind || ok != c.ok {
			t.Errorf("ParsePayload(%q) = (%q, %q, %v), want (%q, %q, %v)", c.in, engine, kind, ok, c.engine, c.kind, c.ok)
		}
	}
}

func TestHubSubscribePublish(t *testing.T) {
	t.Parallel()
	h := NewHub()
	sub, unsub := h.Subscribe("engine-1")
	defer unsub()

	h.Publish("engine-1", KindQueries)
	if got := sub.Take(); len(got) != 1 || got[0] != KindQueries {
		t.Fatalf("Take = %v", got)
	}

	// Another engine's change does not reach this subscriber.
	h.Publish("engine-2", KindCluster)
	if got := sub.Take(); len(got) != 0 {
		t.Fatalf("unexpected pending kinds: %v", got)
	}
}

func TestSubscriptionCoalesces(t *testing.T) {
	t.Parallel()
	h := NewHub()
	sub, unsub := h.Subscribe("e")
	defer unsub()

	h.Publish("e", KindQueries)
	h.Publish("e", KindQueries)
	h.Publish("e", KindQueries)
	h.Publish("e", KindCluster)

	kinds := sub.Take()
	if len(kinds) != 2 {
		t.Fatalf("Take = %v, want both kinds once", kinds)
	}
	seen := map[Kind]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	if !seen[KindQueries] || !seen[KindCluster] {
		t.Fatalf("Take = %v", kinds)
	}
}

func TestHubResync(t *testing.T) {
	t.Parallel()
	h := NewHub()
	sub, unsub := h.Subscribe("e")
	defer unsub()

	h.Resync()
	if got := sub.Take(); len(got) != 2 {
		t.Fatalf("Resync Take = %v", got)
	}
}

func TestHubUnsubscribe(t *testing.T) {
	t.Parallel()
	h := NewHub()
	_, unsub := h.Subscribe("e")
	unsub()
	// Publishing after unsubscribe must not panic.
	h.Publish("e", KindQueries)
}
