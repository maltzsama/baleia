//go:build opa

package opa

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const opaImage = "docker.io/openpolicyagent/opa:1.22.4-static@sha256:12cecac5a174368f7d3f09d3a19d040ebe0c91d576e520335ae7a985a26e39d3"

// TestRego runs `opa test` on deploy/opa/baleia (core semantics and the
// recorded Trino inputs) with the pinned OPA image, inside the cluster.
func TestRego(t *testing.T) {
	src, err := tarDir(filepath.Join(repoRoot(), "deploy", "opa"), "baleia")
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "src.tgz")
	if err := os.WriteFile(tmp, src, 0o600); err != nil {
		t.Fatal(err)
	}
	pod := fmt.Sprintf("opa-test-%d", time.Now().UnixNano())
	mustKubectl(t, "create", "configmap", pod, "--from-file=src.tgz="+tmp)
	t.Cleanup(func() {
		_, _ = kubectl(t.Context(), nil, "delete", "pod,configmap", pod, "--wait=false") // best effort; the namespace is disposable
	})
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata: {name: %[1]s}
spec:
  restartPolicy: Never
  initContainers:
    - name: untar
      image: docker.io/library/alpine:3.20
      command: ["sh", "-ec", "tar -xzf /in/src.tgz -C /src"]
      volumeMounts: [{name: in, mountPath: /in}, {name: src, mountPath: /src}]
  containers:
    - name: opa
      image: %[2]s
      args: ["test", "-v", "/src/baleia"]
      volumeMounts: [{name: src, mountPath: /src}]
  volumes:
    - {name: in, configMap: {name: %[1]s}}
    - {name: src, emptyDir: {}}
`, pod, opaImage)
	if _, err := kubectl(t.Context(), []byte(manifest), "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	var phase string
	err = eventually(t.Context(), 3*time.Minute, func() error {
		out, err := kubectl(t.Context(), nil, "get", "pod", pod, "-o", "jsonpath={.status.phase}")
		if err != nil {
			return err
		}
		phase = out
		if phase != "Succeeded" && phase != "Failed" {
			return fmt.Errorf("phase %s", phase)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := mustKubectl(t, "logs", pod, "-c", "opa")
	if phase != "Succeeded" {
		t.Fatalf("opa test failed:\n%s", logs)
	}
	t.Log(logs)
}

// tarDir packs root/dir into a gzipped tarball.
func tarDir(root, dir string) ([]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	if err != nil {
		return nil, err
	}
	return writeTarball(files)
}
