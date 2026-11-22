//go:build opa

package opa

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Q3. OPA unreachable: every statement fails, trusted identities included;
// nothing runs unfiltered.
func TestFailClosed_OPAUnreachable(t *testing.T) {
	c := connect(t, "svc/trino-opa-down")
	for _, s := range []session{user("u_ana"), as(serviceUser), admin} {
		qerr := c.denied(t, s, "SELECT count(*) FROM tpch.tiny.nation")
		t.Logf("%q: %v", s.user, qerr)
	}
}

// bundleState is the data bundle part of an OPA status report.
type bundleState struct {
	ActiveRevision string
	Code           string
	Message        string
}

func bundleStatus(t testing.TB, addr string) (bundleState, error) {
	status, err := readStatus(t, addr)
	if err != nil {
		return bundleState{}, err
	}
	b := status.Result.Bundles["baleia"]
	msg := b.Message
	for _, e := range b.Errors {
		msg += "; " + e.Message
	}
	return bundleState{ActiveRevision: b.ActiveRevision, Code: b.Code, Message: msg}, nil
}

// serviceEndpoints counts the ready addresses behind a Service.
func serviceEndpoints(t testing.TB, service string) int {
	t.Helper()
	out := mustKubectl(t, "get", "endpointslices", "-l", "kubernetes.io/service-name="+service,
		"-o", `jsonpath={range .items[*].endpoints[*]}{.conditions.ready}{"\n"}{end}`)
	return strings.Count(out, "true")
}

// ordersVisibleTo reads the policied table through c as u_bob.
func ordersVisibleTo(t testing.TB, c *trino, table string) ([]string, *queryError) {
	t.Helper()
	res, err := c.run(t.Context(), user("u_bob"), "SELECT id, region, status, email FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	return res.strings(), res.err
}

// persistPVC is trino-opa-persist from deploy/dev/opa-spike/trino.yaml.
const persistPVC = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: trino-opa-persist
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests: { storage: 64Mi }
`

// Q3 and Q4. The data bundle's life on the coordinator with persistence:
// rejected bundles, unknown formats, restarts with and without the bundle
// service, lost storage, readiness, and a stale bundle.
func TestFailClosed_BundleLifecycle(t *testing.T) {
	setup := connect(t, "svc/trino-baseline") // the Iceberg catalog is shared
	table := seedOrders(t, setup, "orders_lifecycle")
	good := ordersPolicies("life-1", "orders_lifecycle")
	publish(t, good, "trino-opa")
	addr := forward(t, "pod/"+podName(t, "trino-opa"), 8181)
	c := connect(t, "svc/trino-opa")
	if rows, qerr := ordersVisibleTo(t, c, table); qerr != nil || !slices.Equal(rows, []string{"2|US|open|***"}) {
		t.Fatalf("baseline read: %q %v", rows, qerr)
	}

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rejected := []struct {
		name string
		opts bundleOptions
		raw  []byte
	}{
		{"unsigned", bundleOptions{unsigned: true}, nil},
		{"signed with another key", bundleOptions{signWith: otherKey}, nil},
		{"data changed after signing", bundleOptions{tamper: true}, nil},
		{"root outside baleia/bundle", bundleOptions{roots: []string{"baleia"}}, nil},
		{"not a tarball", bundleOptions{}, []byte("garbage")},
	}
	for i, tc := range rejected {
		t.Run("rejected: "+tc.name, func(t *testing.T) {
			tarball := tc.raw
			if tarball == nil {
				b := ordersPolicies(fmt.Sprintf("life-bad-%d", i), "orders_lifecycle")
				b.table("iceberg", "spike", "orders_lifecycle").RowPolicies = nil // would widen bob's view
				if tarball, err = b.build(harness.signingKey, tc.opts); err != nil {
					t.Fatal(err)
				}
			}
			serve(t, tarball)
			// OPA polls every 1-2s; let it try this bundle before reading the
			// status, which may still carry the previous case's error.
			time.Sleep(5 * time.Second)
			err := eventually(t.Context(), time.Minute, func() error {
				st, err := bundleStatus(t, addr)
				if err != nil {
					return err
				}
				if st.Code == "" {
					return errors.New("no bundle error reported yet")
				}
				t.Logf("status: active=%s code=%s message=%s", st.ActiveRevision, st.Code, st.Message)
				if st.ActiveRevision != good.Revision {
					return fmt.Errorf("active revision %s, want %s kept", st.ActiveRevision, good.Revision)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if rows, qerr := ordersVisibleTo(t, c, table); qerr != nil || !slices.Equal(rows, []string{"2|US|open|***"}) {
				t.Errorf("after a rejected bundle: %q %v, want the previous policy", rows, qerr)
			}
		})
	}

	t.Run("unknown format version is activated but not trusted", func(t *testing.T) {
		b := ordersPolicies("life-format-2", "orders_lifecycle")
		b.FormatVersion = 2
		publish(t, b, "trino-opa")
		qerr := c.denied(t, user("u_bob"), "SELECT id FROM "+table)
		t.Logf("format_version 2: %v", qerr)
		c.query(t, admin, "SELECT count(*) FROM "+table)
	})

	t.Run("restart with the bundle service down keeps the persisted bundle", func(t *testing.T) {
		publish(t, good, "trino-opa")
		scale(t, "bundles", 0)
		t.Cleanup(func() { scale(t, "bundles", 1) })
		restartPod(t, "trino-opa")
		if err := waitRollout(t.Context(), "trino-opa"); err != nil {
			t.Fatal(err)
		}
		waitActive(t, "trino-opa", good.Revision)
		c := connect(t, "svc/trino-opa")
		if rows, qerr := ordersVisibleTo(t, c, table); qerr != nil || !slices.Equal(rows, []string{"2|US|open|***"}) {
			t.Errorf("after restart: %q %v", rows, qerr)
		}
	})

	t.Run("lost storage and no valid bundle: not ready, users denied", func(t *testing.T) {
		scale(t, "bundles", 1)
		unsigned, err := good.build(harness.signingKey, bundleOptions{unsigned: true})
		if err != nil {
			t.Fatal(err)
		}
		serve(t, unsigned)
		scale(t, "trino-opa", 0)
		mustKubectl(t, "delete", "pvc", "trino-opa-persist", "--wait=true")
		if _, err := kubectl(t.Context(), []byte(persistPVC), "apply", "-f", "-"); err != nil {
			t.Fatal(err)
		}
		mustKubectl(t, "scale", "deployment/trino-opa", "--replicas=1")
		pod := podName(t, "trino-opa")
		var direct *trino
		err = eventually(t.Context(), 4*time.Minute, func() error {
			// A forward opened before Trino listens dies; open one per try.
			direct = connect(t, "pod/"+pod)
			_, err := direct.run(t.Context(), admin, "SELECT 1")
			return err
		})
		if err != nil {
			t.Fatalf("trino did not start: %v", err)
		}
		// Q4: the client Service routes nothing to a coordinator whose OPA
		// has no active bundle.
		if n := serviceEndpoints(t, "trino-opa"); n != 0 {
			t.Errorf("trino-opa has %d ready endpoints, want 0", n)
		}
		qerr := direct.denied(t, user("u_bob"), "SELECT id FROM "+table)
		t.Logf("user without a bundle: %v", qerr)
		res, err := direct.run(t.Context(), as(serviceUser), "SELECT count(*) FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("service identity without a bundle: rows=%q error=%v", res.strings(), res.err)
	})

	t.Run("readiness follows activation", func(t *testing.T) {
		publish(t, good, "trino-opa")
		if err := waitRollout(t.Context(), "trino-opa", "trino-opa-worker"); err != nil {
			t.Fatal(err)
		}
		if n := serviceEndpoints(t, "trino-opa"); n != 1 {
			t.Errorf("trino-opa has %d ready endpoints, want 1", n)
		}
		c := connect(t, "svc/trino-opa")
		if rows, qerr := ordersVisibleTo(t, c, table); qerr != nil || !slices.Equal(rows, []string{"2|US|open|***"}) {
			t.Errorf("after activation: %q %v", rows, qerr)
		}
	})

	t.Run("a stale bundle keeps being enforced while the service is unreachable", func(t *testing.T) {
		// Published: bob loses his row. OPA cannot fetch it and keeps
		// enforcing the previous revision; only its status shows it.
		addr := forward(t, "pod/"+podName(t, "trino-opa"), 8181)
		scale(t, "bundles", 0)
		t.Cleanup(func() { scale(t, "bundles", 1) })
		err := eventually(t.Context(), time.Minute, func() error {
			st, err := bundleStatus(t, addr)
			if err != nil {
				return err
			}
			if st.Code == "" {
				return errors.New("no download error reported yet")
			}
			t.Logf("status: active=%s code=%s message=%s", st.ActiveRevision, st.Code, st.Message)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		c := connect(t, "svc/trino-opa")
		rows, qerr := ordersVisibleTo(t, c, table)
		t.Logf("with a published revision OPA cannot fetch: rows=%q error=%v", rows, qerr)
	})
}
