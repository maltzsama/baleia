//go:build opa

package opa

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// Identities. The admin is the only login Trino accepts for impersonation
// (baleia-rules.json); every other identity is Baleia's X-Trino-User.
const (
	adminUser   = "baleia_admin"
	serviceUser = "baleia"
	outsider    = "outsider" // a second password login, not allowed to impersonate
)

// session is one Trino identity as Baleia sends it.
type session struct {
	user        string            // X-Trino-User
	login       string            // basic-auth user; adminUser when empty
	credentials map[string]string // X-Trino-Extra-Credential
}

func as(user string) session { return session{user: user} }

func (s session) with(key, value string) session {
	creds := map[string]string{key: value}
	for k, v := range s.credentials {
		creds[k] = v
	}
	s.credentials = creds
	return s
}

// trino talks to one coordinator through a port-forward.
type trino struct {
	base   string
	client *http.Client
}

// connect forwards to target (svc/x or pod/x) on 8443.
func connect(t testing.TB, target string) *trino {
	t.Helper()
	return &trino{
		base: "https://" + forward(t, target, 8443),
		client: &http.Client{
			Timeout:   2 * time.Minute,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: harness.caPool, ServerName: "localhost"}},
		},
	}
}

// result is a finished statement.
type result struct {
	queryID string
	columns []string
	rows    [][]any
	err     *queryError
	elapsed time.Duration
}

type queryError struct {
	Message   string `json:"message"`
	ErrorName string `json:"errorName"`
	ErrorType string `json:"errorType"`
}

func (e *queryError) Error() string { return e.ErrorName + ": " + e.Message }

type queryResults struct {
	ID      string `json:"id"`
	NextURI string `json:"nextUri"`
	Columns []struct {
		Name string `json:"name"`
	} `json:"columns"`
	Data  [][]any     `json:"data"`
	Error *queryError `json:"error"`
}

// run executes sql as s and follows nextUri to the end. A query error is in
// result.err; err is only for transport failures.
func (c *trino) run(ctx context.Context, s session, sql string) (*result, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/statement", strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	login := s.login
	if login == "" {
		login = adminUser
	}
	req.SetBasicAuth(login, harness.passwords[login])
	if s.user != "" {
		req.Header.Set("X-Trino-User", s.user)
	}
	// Values are URL-encoded, as Baleia sends them (a scope is a comma list).
	for _, k := range slices.Sorted(maps.Keys(s.credentials)) {
		req.Header.Add("X-Trino-Extra-Credential", k+"="+url.QueryEscape(s.credentials[k]))
	}
	res := &result{}
	for {
		page, err := c.do(req)
		if err != nil {
			return nil, err
		}
		res.queryID = page.ID
		if len(page.Columns) > 0 && res.columns == nil {
			for _, col := range page.Columns {
				res.columns = append(res.columns, col.Name)
			}
		}
		res.rows = append(res.rows, page.Data...)
		if page.Error != nil {
			res.err = page.Error
		}
		if page.NextURI == "" {
			break
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, page.NextURI, nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(login, harness.passwords[login])
	}
	res.elapsed = time.Since(start)
	return res, nil
}

func (c *trino) do(req *http.Request) (*queryResults, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trino: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var page queryResults
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("trino: decoding %q: %w", body, err)
	}
	return &page, nil
}

// query runs sql and fails the test on any error.
func (c *trino) query(t testing.TB, s session, sql string) *result {
	t.Helper()
	res, err := c.run(t.Context(), s, sql)
	if err != nil {
		t.Fatalf("%s as %s: %v", sql, s.user, err)
	}
	if res.err != nil {
		t.Fatalf("%s as %s: %v", sql, s.user, res.err)
	}
	return res
}

// denied runs sql and fails the test unless Trino refuses it.
func (c *trino) denied(t testing.TB, s session, sql string) *queryError {
	t.Helper()
	res, err := c.run(t.Context(), s, sql)
	if err != nil {
		t.Fatalf("%s as %s: %v", sql, s.user, err)
	}
	if res.err == nil {
		t.Fatalf("%s as %s: succeeded with %v, want an error", sql, s.user, res.rows)
	}
	return res.err
}

// strings flattens a result to "a|b" lines, in order.
func (r *result) strings() []string {
	out := make([]string, 0, len(r.rows))
	for _, row := range r.rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(cells, "|"))
	}
	return out
}
