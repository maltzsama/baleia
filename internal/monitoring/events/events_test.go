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
	ch, unsub := h.Subscribe("engine-1")
	defer unsub()

	h.Publish(Event{EngineID: "engine-1", Kind: KindQueries})
	select {
	case ev := <-ch:
		if ev.EngineID != "engine-1" || ev.Kind != KindQueries {
			t.Fatalf("got %+v", ev)
		}
	default:
		t.Fatal("no event delivered")
	}

	// Another engine's change does not reach this subscriber.
	h.Publish(Event{EngineID: "engine-2", Kind: KindCluster})
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

func TestHubUnsubscribe(t *testing.T) {
	t.Parallel()
	h := NewHub()
	_, unsub := h.Subscribe("e")
	unsub()
	// Publishing after unsubscribe must not panic.
	h.Publish(Event{EngineID: "e", Kind: KindQueries})
}
