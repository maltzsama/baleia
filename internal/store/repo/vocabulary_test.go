package repo

import (
	"fmt"
	"slices"
	"testing"
)

func TestVocabularies_GetNeedsTheSameVersionAndPrefix(t *testing.T) {
	t.Parallel()
	var c vocabularies
	if _, ok := c.get("metadata:a", "v1", "or"); ok {
		t.Fatal("an empty cache has nothing")
	}
	c.put("metadata:a", "v1", "or", []string{"orders"})
	if got, ok := c.get("metadata:a", "v1", "or"); !ok || !slices.Equal(got, []string{"orders"}) {
		t.Fatalf("get at the stored version and prefix = %v, %v", got, ok)
	}
	if _, ok := c.get("metadata:a", "v1", "cu"); ok {
		t.Error("another prefix was not read")
	}
	if _, ok := c.get("metadata:a", "v2", "or"); ok {
		t.Error("terms read at another version are stale")
	}
	// A prefix with no term is a read too: it is not read again.
	c.put("metadata:a", "v1", "zz", []string{})
	if got, ok := c.get("metadata:a", "v1", "zz"); !ok || len(got) != 0 {
		t.Errorf("an empty prefix is remembered: %v, %v", got, ok)
	}
}

// A scope read at a new version starts over: the prefixes of the old one go.
func TestVocabularies_NewVersionStartsOver(t *testing.T) {
	t.Parallel()
	var c vocabularies
	c.put("metadata:a", "v1", "or", []string{"orders"})
	c.put("metadata:a", "v1", "cu", []string{"customers"})
	c.put("metadata:a", "v2", "or", []string{"orders", "orderz"})
	if _, ok := c.get("metadata:a", "v2", "cu"); ok {
		t.Error("a prefix of the old version leaked into the new one")
	}
	if got, ok := c.get("metadata:a", "v2", "or"); !ok || len(got) != 2 {
		t.Errorf("the new version keeps its own terms: %v, %v", got, ok)
	}
}

// A process keeps the scopes it searches, at most maxVocabularyScopes, and
// drops the one used least recently.
func TestVocabularies_EvictsTheLeastRecentlyUsed(t *testing.T) {
	t.Parallel()
	var c vocabularies
	for i := range maxVocabularyScopes {
		c.put(fmt.Sprintf("metadata:%03d", i), "v", "or", []string{"orders"})
	}
	// Use the first one, so the second is the oldest.
	if _, ok := c.get("metadata:000", "v", "or"); !ok {
		t.Fatal("the first scope is cached")
	}
	c.put("metadata:new", "v", "or", []string{"orders"})
	if _, ok := c.get("metadata:001", "v", "or"); ok {
		t.Error("the least recently used scope stays")
	}
	for _, key := range []string{"metadata:000", "metadata:new"} {
		if _, ok := c.get(key, "v", "or"); !ok {
			t.Errorf("%s was evicted", key)
		}
	}
	if len(c.entries) != maxVocabularyScopes {
		t.Errorf("%d scopes cached, want %d", len(c.entries), maxVocabularyScopes)
	}
}
