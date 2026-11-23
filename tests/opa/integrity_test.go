//go:build opa

package opa

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Q7. A Rego release tarball whose checksum does not match stops the
// verify-policy init container, so OPA never starts with it.
func TestIntegrity_TamperedRegoStopsTheInitContainer(t *testing.T) {
	tarball, _, err := policyTarball()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tampered := append(append([]byte{}, tarball...), 0) // one extra byte
	if err := os.WriteFile(filepath.Join(dir, "baleia-opa-policy.tar.gz"), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	// The checksum the release published (of the untampered tarball).
	release := mustKubectl(t, "get", "deployment", "trino-opa", "-o", `jsonpath={.spec.template.spec.volumes[?(@.name=="release")].configMap.name}`)
	sha := strings.TrimSpace(mustKubectl(t, "get", "configmap", release, "-o", `jsonpath={.data.baleia-opa-policy\.tar\.gz\.sha256}`))
	if err := os.WriteFile(filepath.Join(dir, "baleia-opa-policy.tar.gz.sha256"), []byte(sha+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("tampered-%d", time.Now().UnixNano())
	mustKubectl(t, "create", "configmap", name,
		"--from-file="+filepath.Join(dir, "baleia-opa-policy.tar.gz"),
		"--from-file="+filepath.Join(dir, "baleia-opa-policy.tar.gz.sha256"))
	t.Cleanup(func() { mustKubectl(t, "delete", "pod,configmap", name, "--wait=false") })

	// The same init container as trino-opa's, then a container that would
	// only start if it passed.
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata: {name: %[1]s}
spec:
  restartPolicy: Never
  initContainers:
    - name: verify-policy
      image: docker.io/library/alpine:3.20
      command: ["/bin/sh", "-ec"]
      args:
        - |
          cp /release/baleia-opa-policy.tar.gz /policy/policy.tar.gz
          echo "$(cat /release/baleia-opa-policy.tar.gz.sha256)  /policy/policy.tar.gz" | sha256sum -c - || { rm -f /policy/policy.tar.gz; exit 1; }
      volumeMounts: [{name: release, mountPath: /release}, {name: policy, mountPath: /policy}]
  containers:
    - name: opa
      image: %[2]s
      args: ["version"]
  volumes:
    - {name: release, configMap: {name: %[1]s}}
    - {name: policy, emptyDir: {}}
`, name, opaImage)
	if _, err := kubectl(t.Context(), []byte(manifest), "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	err = eventually(t.Context(), 2*time.Minute, func() error {
		out, err := kubectl(t.Context(), nil, "get", "pod", name, "-o",
			`jsonpath={.status.phase} {.status.initContainerStatuses[0].state.terminated.exitCode}`)
		if err != nil {
			return err
		}
		fields := strings.Fields(out)
		if len(fields) != 2 {
			return fmt.Errorf("init container not finished: %q", out)
		}
		if fields[0] != "Failed" || fields[1] == "0" {
			return fmt.Errorf("pod %s, init exit code %s; want Failed with a non-zero exit", fields[0], fields[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(mustKubectl(t, "logs", name, "-c", "verify-policy"))
}

// Q7. OPA reports the Rego digest (the policy bundle's manifest revision) in
// its status, next to the data bundle's revision; Baleia compares it with the
// digest of the release the engine is pinned to.
func TestIntegrity_PolicyDigestIsReported(t *testing.T) {
	_, expected, err := policyTarball()
	if err != nil {
		t.Fatal(err)
	}
	if expected != harness.policyDigest {
		t.Fatalf("deploy/opa/baleia changed since the namespace was created (digest %s, deployed %s): rerun without OPA_SPIKE_REUSE", expected, harness.policyDigest)
	}
	for _, deployment := range []string{"trino-opa", "trino-opa-worker", "trino-opa-single"} {
		status, err := readStatus(t, forward(t, "pod/"+podName(t, deployment), 8181))
		if err != nil {
			t.Fatal(err)
		}
		if got := status.Result.Bundles["policy"].ActiveRevision; got != expected {
			t.Errorf("%s reports policy %q, want %q", deployment, got, expected)
		}
	}
}

// Q7. Every ConfigMap with Trino or OPA security configuration (Trino's
// access control, OPA's configuration, the Rego release) is immutable.
func TestIntegrity_SecurityConfigMapsAreImmutable(t *testing.T) {
	out := mustKubectl(t, "get", "configmaps", "-o", `jsonpath={range .items[*]}{.metadata.name} {.immutable}{"\n"}{end}`)
	found := map[string]bool{}
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		prefix, ok := securityConfigMap(fields[0])
		if !ok {
			continue
		}
		found[prefix] = true
		if len(fields) != 2 || fields[1] != "true" {
			t.Errorf("%s is not immutable", fields[0])
		}
		_, err := kubectl(t.Context(), nil, "patch", "configmap", fields[0], "--type=merge",
			"-p", `{"data":{"opa-batched.properties":"access-control.name=allow-all\n"}}`)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("patching %s: %v, want an immutable-field error", fields[0], err)
		}
	}
	for _, prefix := range securityConfigMaps {
		if !found[prefix] {
			t.Errorf("no %s* ConfigMap", prefix)
		}
	}
}

var securityConfigMaps = []string{"trino-security-", "opa-config-", "opa-policy-release-"}

// securityConfigMap reports whether name is a content-named security
// ConfigMap, and its prefix.
func securityConfigMap(name string) (string, bool) {
	for _, prefix := range securityConfigMaps {
		if strings.HasPrefix(name, prefix) {
			return prefix, true
		}
	}
	return "", false
}

// Q7. OPA's data and policy API listens on the pod's loopback only: nothing
// on the cluster network can query it or PUT a policy. The diagnostic port
// serves health and metrics.
func TestIntegrity_OPAAPIIsLocalOnly(t *testing.T) {
	ip := strings.TrimSpace(mustKubectl(t, "get", "pod", podName(t, "trino-opa"), "-o", "jsonpath={.status.podIP}"))
	name := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	script := fmt.Sprintf(`
if wget -q -T 3 -O- http://%[1]s:8181/v1/data >/dev/null 2>&1; then echo data=open; else echo data=closed; fi
if wget -q -T 3 -O- http://%[1]s:8282/health >/dev/null 2>&1; then echo health=open; else echo health=closed; fi
`, ip)
	out, err := kubectl(t.Context(), nil, "run", name, "--rm", "-i", "--restart=Never", "--quiet",
		"--image=docker.io/library/alpine:3.20", "--", "sh", "-c", script)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(out)
	if !strings.Contains(out, "data=closed") {
		t.Error("OPA's data API is reachable from the cluster network")
	}
	if !strings.Contains(out, "health=open") {
		t.Error("OPA's diagnostic port is not reachable for probes")
	}
	if strings.TrimSpace(out) == "" {
		t.Error(errors.New("probe printed nothing"))
	}
}
