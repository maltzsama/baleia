package repo

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/search"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// The fuzzy fallback of a search compares a misspelled word with the terms of a
// scope that start with its first letters. Those terms change only when the
// scope is rebuilt, so each process keeps them in memory, by scope and prefix,
// tied to the version of the scope they were read at. A search then costs one
// primary-key lookup of the scopes' versions, and the terms of a prefix are read
// once per version, by whichever search needs them first, with the same bounded
// range query as before: nothing reads a whole scope (ADR 0023).
const (
	// maxVocabularyScopes bounds how many scopes a process keeps.
	maxVocabularyScopes = 256
)

// vocabulary is what a process knows of one scope's terms: those that start
// with each prefix it was asked for, at one version of the scope.
type vocabulary struct {
	version  string
	prefixes map[string][]string
	used     time.Time
}

// vocabularies are the vocabularies a Repo keeps in memory, by scope key.
type vocabularies struct {
	mu      sync.Mutex
	entries map[string]*vocabulary
}

// get returns the terms of the scope that start with prefix, if they were read
// at version.
func (c *vocabularies) get(key, version, prefix string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	if !ok || v.version != version {
		return nil, false
	}
	terms, ok := v.prefixes[prefix]
	if ok {
		v.used = time.Now()
	}
	return terms, ok
}

// put stores the terms of a prefix read at version. A scope read at another
// version starts over, and the scope used least recently is dropped when there
// are more than maxVocabularyScopes.
func (c *vocabularies) put(key, version, prefix string, terms []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*vocabulary{}
	}
	v, ok := c.entries[key]
	if !ok || v.version != version {
		v = &vocabulary{version: version, prefixes: map[string][]string{}}
		c.entries[key] = v
	}
	v.prefixes[prefix] = terms
	v.used = time.Now()
	for len(c.entries) > maxVocabularyScopes {
		oldest, at := "", time.Time{}
		for k, e := range c.entries {
			if k != key && (oldest == "" || e.used.Before(at)) {
				oldest, at = k, e.used
			}
		}
		delete(c.entries, oldest)
	}
}

// scopeVersions are the versions of the scopes' terms: what a scope was last
// built from and when. A scope that was never built has none.
func scopeVersions(tx *gorm.DB, scopes []string) (map[string]string, error) {
	var rows []model.SearchScope
	err := tx.Select("scope_key", "fingerprint", "indexed_at").Where("scope_key IN ?", scopes).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read search scope versions: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.IndexedAt != nil {
			out[row.ScopeKey] = row.Fingerprint + "|" + strconv.FormatInt(row.IndexedAt.UTC().UnixMicro(), 10)
		}
	}
	return out, nil
}

// termsWithPrefix are the terms of the scope, at version, that start with
// prefix: from memory when they were read at that version, otherwise read once.
// The read uses the caller's own transaction: a search holds a connection while
// it runs, and waiting for a second one (to read on the side, or for another
// search to read) can starve the pool when searches are many. Searches that find
// a prefix missing at the same time each read it, a bounded range query, and the
// last to finish keeps its copy.
func (r *Repo) termsWithPrefix(tx *gorm.DB, key, version, prefix string) ([]string, error) {
	if terms, ok := r.vocabularies.get(key, version, prefix); ok {
		return terms, nil
	}
	var terms []string
	err := tx.Model(&model.SearchTerm{}).
		Distinct("term").Where("scope_key = ?", key).Where(prefixLike([]string{prefix})).
		Limit(maxFuzzyTerms).Pluck("term", &terms).Error
	if err != nil {
		return nil, fmt.Errorf("read search vocabulary %s: %w", key, err)
	}
	r.vocabularies.put(key, version, prefix, terms)
	return terms, nil
}

// nearTerms are the terms of scopes that look like a misspelling of a query
// word: those sharing its first letters and close by trigram similarity. They
// come from the scopes' in-memory vocabularies.
func (r *Repo) nearTerms(tx *gorm.DB, scopes []string, p search.Pattern) ([]string, error) {
	prefixes := p.FuzzyPrefixes()
	if len(prefixes) == 0 {
		return nil, nil
	}
	versions, err := scopeVersions(tx, scopes)
	if err != nil {
		return nil, err
	}
	near := []string{}
	seen := map[string]struct{}{}
	for _, key := range scopes {
		version, built := versions[key]
		if !built {
			continue
		}
		for _, prefix := range prefixes {
			terms, err := r.termsWithPrefix(tx, key, version, prefix)
			if err != nil {
				return nil, err
			}
			for _, term := range terms {
				if _, ok := seen[term]; ok || !p.IsNear(term) {
					continue
				}
				seen[term] = struct{}{}
				near = append(near, term)
			}
		}
	}
	return near, nil
}
