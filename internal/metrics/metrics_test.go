package metrics

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// allowedLabelNames is the closed set of label names this package may use.
// Adding a label outside this set is a cardinality bug, not a refactor.
var allowedLabelNames = map[string]bool{
	"route":        true,
	"method":       true,
	"status_class": true,
	"outcome":      true,
	"engine":       true,
	"adapter":      true,
	"code":         true,
	"operation":    true,
	"cache":        true,
}

var (
	uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sqlRe  = regexp.MustCompile(`(?i)\b(select\s|insert\s+into|update\s+\S+\s+set|delete\s+from|create\s+table|drop\s+table)\b`)
)

func gatherLabelValues(t *testing.T) (names []string, values []string) {
	t.Helper()
	families, err := Registry.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		// Only this package's own metrics: the Go/process collectors ship
		// their own label sets that are not ours to police.
		if !strings.HasPrefix(mf.GetName(), "baleia_") {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				names = append(names, lp.GetName())
				values = append(values, lp.GetValue())
			}
		}
	}
	return names, values
}

func TestLabelNamesAreFromTheAllowedSet(t *testing.T) {
	// Populate every vector the way production code does.
	HTTPRequests.WithLabelValues("/api/v1/workspaces/{id}/executions", "GET", "2xx").Inc()
	HTTPDuration.WithLabelValues("/api/v1/workspaces/{id}/executions", "GET").Observe(0.01)
	AuthAttempts.WithLabelValues("success").Inc()
	EngineQueries.WithLabelValues("trino", "trino_rest", "success").Inc()
	EngineErrors.WithLabelValues("trino", "timeout").Inc()
	StorageOperations.WithLabelValues("put", "success").Inc()
	StorageBytes.WithLabelValues("put").Add(128)
	CacheLookups.WithLabelValues("catalogs", "hit").Inc()

	names, _ := gatherLabelValues(t)
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
		require.True(t, allowedLabelNames[n], "label %q is not in the bounded allow-list", n)
	}

	// The vectors we just exercised must actually be present.
	for _, want := range []string{"route", "method", "status_class", "outcome", "engine", "adapter", "code", "operation", "cache"} {
		require.True(t, seen[want], "expected label %q to be exercised by this test", want)
	}
}

// TestLabelValuesCarryNoCardinalityOrPII guards the acceptance criterion: no
// SQL, user id, execution id, email, table or catalog name may ever appear as
// a label value. If a future change starts interpolating one of those into a
// label, this test fails.
func TestLabelValuesCarryNoCardinalityOrPII(t *testing.T) {
	// Values that must never reach a label, however a future change wires them.
	const (
		sql         = "SELECT secret_column FROM billing.customers WHERE ssn = '123-45-6789'"
		userID      = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
		executionID = "b1234567-89ab-4cde-8f01-234567890abc"
		email       = "ana.silva@example.com"
		catalogName = "finance_prod_iceberg"
		tableName   = "customers_eu"
	)

	HTTPRequests.WithLabelValues("/api/v1/workspaces/{id}/executions", "GET", "2xx").Inc()
	AuthAttempts.WithLabelValues("invalid_token").Inc()
	EngineQueries.WithLabelValues("trino", "trino_rest", "error").Inc()
	EngineErrors.WithLabelValues("trino", "trino").Inc()
	StorageOperations.WithLabelValues("put", "error").Inc()
	CacheLookups.WithLabelValues("catalogs", "miss").Inc()

	_, values := gatherLabelValues(t)
	joined := strings.Join(values, "\n")

	for _, secret := range []string{sql, userID, executionID, email, catalogName, tableName} {
		require.NotContains(t, joined, secret, "a label value leaked a high-cardinality or sensitive value")
	}
	for _, v := range values {
		require.False(t, uuidRe.MatchString(v), "label value %q looks like an id", v)
		require.False(t, strings.Contains(v, "@"), "label value %q looks like an email", v)
		require.False(t, sqlRe.MatchString(v), "label value %q looks like SQL", v)
		require.NotContains(t, v, " ", fmt.Sprintf("label value %q contains whitespace", v))
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		199: "other", 200: "2xx", 204: "2xx", 301: "3xx", 399: "3xx",
		400: "4xx", 404: "4xx", 499: "4xx", 500: "5xx", 503: "5xx",
	}
	for code, want := range cases {
		require.Equal(t, want, StatusClass(code), "status %d", code)
	}
}
