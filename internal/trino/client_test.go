package trino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildCreateCatalogDDL_RedactsSecretKeys(t *testing.T) {
	t.Parallel()
	spec := CatalogSpec{
		Name:      "baleia_polaris",
		Connector: "iceberg",
		Properties: map[string]string{
			"iceberg.rest-catalog.uri":               "http://polaris:8181/api/catalog",
			"iceberg.rest-catalog.oauth2.credential": "abc:def", // secret
			"fs.s3.enabled":                          "true",
		},
		SecretKeys: []string{"iceberg.rest-catalog.oauth2.credential"},
	}
	ddl := buildCreateCatalogDDL(spec)

	// The placeholder must appear in the DDL text where the secret was.
	if !strings.Contains(ddl, "@baleia-secret[baleia_polaris:iceberg.rest-catalog.oauth2.credential]") {
		t.Errorf("DDL missing placeholder:\n%s", ddl)
	}

	// The real secret must NEVER appear in the DDL text.
	if strings.Contains(ddl, "abc:def") {
		t.Errorf("DDL contains the real secret!\n%s", ddl)
	}

	// The non-secret values must appear.
	if !strings.Contains(ddl, "http://polaris:8181/api/catalog") {
		t.Errorf("DDL missing non-secret value:\n%s", ddl)
	}
	if !strings.Contains(ddl, "fs.s3.enabled") {
		t.Errorf("DDL missing fs.s3.enabled:\n%s", ddl)
	}
}

func TestBuildCreateCatalogDDL_SortedKeys(t *testing.T) {
	t.Parallel()
	// Keys out of alphabetical order on purpose: at runtime Go's range order
	// is randomized, so the only way to detect a regression to range-order
	// is to make this test fail when the output is not alphabetically sorted.
	spec := CatalogSpec{
		Name:      "x",
		Connector: "tpch",
		Properties: map[string]string{
			"zzz": "3",
			"aaa": "1",
			"mmm": "2",
		},
	}
	ddl := buildCreateCatalogDDL(spec)

	// Extract the order of property keys as they appear in the DDL.
	// They should be aaa, mmm, zzz.
	wantOrder := []string{"\"aaa\"", "\"mmm\"", "\"zzz\""}
	pos := 0
	for _, want := range wantOrder {
		idx := strings.Index(ddl[pos:], want)
		if idx < 0 {
			t.Errorf("key %s not found after position %d in DDL:\n%s", want, pos, ddl)
			return
		}
		pos += idx + len(want)
	}

	// Sanity check: the full DDL should be deterministic regardless of map
	// iteration order. Re-run and confirm it matches.
	ddl2 := buildCreateCatalogDDL(spec)
	if ddl != ddl2 {
		t.Errorf("DDL is non-deterministic across calls:\nDDL1:\n%s\nDDL2:\n%s", ddl, ddl2)
	}
}

func TestBuildCreateCatalogDDL_EscapesSingleQuote(t *testing.T) {
	t.Parallel()
	// A password like "abc'def" must be escaped to "abc''def" in the DDL.
	// Without escaping, the value would terminate the SQL string literal
	// and either break the DDL or open an injection path.
	spec := CatalogSpec{
		Name:      "pg_test",
		Connector: "postgresql",
		Properties: map[string]string{
			"connection-url":      "jdbc:postgresql://host/db",
			"connection-user":     "alice",
			"connection-password": "abc'def",
		},
		SecretKeys: []string{"connection-password"},
	}
	ddl := buildCreateCatalogDDL(spec)

	// The password is a secret key, so it is placeholderized, not escaped;
	// the escape path is tested with a non-secret value below.

	// Non-secret: connection-url contains no quote, but let's also test
	// an explicit non-secret with a single quote to exercise the escape path.
	spec2 := CatalogSpec{
		Name:      "pg_test",
		Connector: "postgresql",
		Properties: map[string]string{
			"connection-url": "jdbc:postgresql://host'db",
		},
	}
	ddl2 := buildCreateCatalogDDL(spec2)
	if !strings.Contains(ddl2, "'jdbc:postgresql://host''db'") {
		t.Errorf("single quote not escaped in DDL:\n%s", ddl2)
	}

	_ = ddl // reference to avoid unused warning
}

func TestValidateCatalogName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ok   bool
	}{
		{"", false},
		{"a", true},
		{"baleia_polaris", true},
		{"Baleia_Polaris", false}, // uppercase
		{"baleia-polaris", false}, // hyphen
		{"1baleia", false},        // starts with digit
		{"system", true},          // regex allows it; reserved check is elsewhere
		{"_baleia", false},        // starts with underscore
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false}, // too long
	}
	for _, c := range cases {
		err := validateCatalogName(c.name)
		if (err == nil) != c.ok {
			t.Errorf("validateCatalogName(%q) err=%v; want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestCreateCatalog_RejectsInvalidNameBeforeExecute(t *testing.T) {
	t.Parallel()
	c := &Client{baseURL: "http://invalid:1", user: "test", http: nil}
	err := c.CreateCatalog(context.Background(), CatalogSpec{
		Name:       "BadName",
		Connector:  "iceberg",
		Properties: map[string]string{"a": "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid catalog name") {
		t.Errorf("expected validation error, got %v", err)
	}
}

// TestExecute_CancelSendsDelete pins the orphaned-query fix: when the context
// is cancelled mid-poll, the client abandons the query on Trino with exactly
// one DELETE to the last nextUri before returning ctx.Err().
func TestExecute_CancelSendsDelete(t *testing.T) {
	t.Parallel()
	var deleteCalls int32
	polling := make(chan struct{}, 1)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statement":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fmt.Sprintf(`{"id":"20240101_000000_00000_abcde","nextUri":%q}`, srv.URL+"/next1"))
		case r.Method == http.MethodGet && r.URL.Path == "/next1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fmt.Sprintf(`{"nextUri":%q}`, srv.URL+"/next2"))
		case r.Method == http.MethodGet && r.URL.Path == "/next2":
			// Simulate a long-running query: block until the client aborts.
			select {
			case polling <- struct{}{}:
			default:
			}
			<-r.Context().Done()
		case r.Method == http.MethodDelete && r.URL.Path == "/next2":
			atomic.AddInt32(&deleteCalls, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "testuser")

	// Cancel once the client is polling the running query, not after a guess.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-polling
		cancel()
	}()

	_, err := c.Execute(ctx, "SELECT 1", "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&deleteCalls) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&deleteCalls); got != 1 {
		t.Fatalf("expected exactly one DELETE to /next2 on cancel, got %d", got)
	}
}

// TestKillQuery_RejectsBadID pins the injection guard on kill_query: an id that
// does not match Trino's format returns an error and never reaches HTTP.
func TestKillQuery_RejectsBadID(t *testing.T) {
	t.Parallel()
	c := &Client{baseURL: "http://invalid:1", user: "test", http: &http.Client{}}

	err := c.KillQuery(context.Background(), "not-an-id", "cancel")
	if err == nil || !strings.Contains(err.Error(), "invalid trino query id") {
		t.Fatalf("expected an id validation error, got %v", err)
	}
}

// TestExecuteOptions_UserSentAsIdentity pins the per-user identity fix: a
// statement executed with ExecuteOptions.User reaches Trino with that
// X-Trino-User, not the client's configured user.
func TestExecuteOptions_UserSentAsIdentity(t *testing.T) {
	t.Parallel()
	var gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/statement" {
			gotUser = r.Header.Get("X-Trino-User")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"20240101_000000_00000_abcde","nextUri":""}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "baleia")
	if _, err := c.ExecuteWithOptions(context.Background(), "SELECT 1",
		ExecuteOptions{User: "u_123e4567-e89b-12d3-a456-426614174000"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotUser != "u_123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("X-Trino-User = %q, want the per-call user", gotUser)
	}
}

// TestDecodeUseNumber pins the precision fix: JSON decoding must keep bigint
// values as json.Number so values above 2^53 survive the wire intact.
func TestDecodeUseNumber(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/v1/statement" {
			_, _ = io.WriteString(w, `{"id":"20240101_000000_00000_abcde","columns":[{"name":"n","type":"bigint"}],"data":[[9007199254740993]]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "testuser")
	res, err := c.Execute(context.Background(), "SELECT 9007199254740993", "", "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Data) != 1 || len(res.Data[0]) != 1 {
		t.Fatalf("unexpected data shape: %#v", res.Data)
	}
	num, ok := res.Data[0][0].(json.Number)
	if !ok {
		t.Fatalf("cell type = %T, want json.Number", res.Data[0][0])
	}
	if num.String() != "9007199254740993" {
		t.Fatalf("bigint lost precision: %s", num.String())
	}
}

// TestStatementAsksForParametricDatetime: without the capability Trino rounds
// timestamp(6) and time(6) values to milliseconds.
func TestStatementAsksForParametricDatetime(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Trino-Client-Capabilities")
		_, _ = io.WriteString(w, `{"id":"20240101_000000_00000_abcde"}`)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "baleia").Execute(context.Background(), "SELECT 1", "", ""); err != nil {
		t.Fatal(err)
	}
	if got != "PARAMETRIC_DATETIME" {
		t.Fatalf("X-Trino-Client-Capabilities = %q", got)
	}
}

// TestErrorsAreTyped: statement failures carry Trino's classification and
// HTTP failures are transport errors.
func TestErrorsAreTyped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Trino-User") {
		case "down":
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		case "anon":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "Unauthorized")
			return
		}
		_, _ = io.WriteString(w, `{"id":"q","error":{"message":"m","errorName":"SYNTAX_ERROR","errorType":"USER_ERROR"}}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "baleia").Execute(context.Background(), "SELEKT", "", "")
	var qe *QueryError
	if !errors.As(err, &qe) || qe.ErrorName != "SYNTAX_ERROR" || qe.ErrorType != "USER_ERROR" || qe.QueryID != "q" {
		t.Fatalf("err = %#v", err)
	}
	_, err = New(srv.URL, "down").Execute(context.Background(), "SELECT 1", "", "")
	var te *TransportError
	if !errors.As(err, &te) || te.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %#v", err)
	}
	_, err = New(srv.URL, "anon").Execute(context.Background(), "SELECT 1", "", "")
	if !errors.As(err, &te) || te.Status != http.StatusUnauthorized {
		t.Fatalf("a non-JSON 401 must be a transport error, got %#v", err)
	}
}
