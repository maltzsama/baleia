//go:build opa

// Package opa is the spike suite for #994 (ADR 0028): Trino 484 with the
// catalog guard and the OPA access control, on the minikube dev cluster.
//
// Run it with:
//
//	go test -tags opa -count=1 -timeout 60m ./tests/opa/
//
// It applies deploy/dev/opa-spike into the namespace opa-spike (deleting any
// earlier copy), runs, and leaves the namespace up for inspection. Set
// OPA_SPIKE_REUSE=1 to run against a namespace a previous run left up, and
// OPA_SPIKE_RECORD=1 to rewrite the captured plugin inputs under
// deploy/opa/baleia/testdata.
package opa

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const namespace = "opa-spike"

// repoRoot is the repository root, found from this file's location.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("opa: cannot locate the test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// kubectl runs kubectl in the spike namespace and returns its stdout.
func kubectl(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"-n", namespace}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// mustKubectl also works in t.Cleanup, where t.Context() is already done.
func mustKubectl(t testing.TB, args ...string) string {
	t.Helper()
	out, err := kubectl(context.WithoutCancel(t.Context()), nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// eventually retries check until it returns nil or the timeout passes.
func eventually(ctx context.Context, timeout time.Duration, check func() error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		err := check()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("after %s: %w", timeout, err)
		case <-time.After(time.Second):
		}
	}
}

var forwardLine = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) ->`)

// forward port-forwards target (svc/x, pod/x, deploy/x) to a free local port
// and returns its address. The forward lives until the test ends. A pod
// forward reaches the pod's loopback, so it also reaches OPA's
// localhost-only API, and it works on a pod that is not ready.
func forward(t testing.TB, target string, port int) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "kubectl", "-n", namespace, "port-forward", "--address", "127.0.0.1", target, fmt.Sprintf(":%d", port))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait() // the forward is killed on purpose; its exit status says nothing
	})
	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if m := forwardLine.FindStringSubmatch(scanner.Text()); m != nil {
				found <- "127.0.0.1:" + m[1]
				break
			}
		}
		_, _ = io.Copy(io.Discard, stdout) // keep draining so kubectl never blocks
	}()
	select {
	case addr := <-found:
		return addr
	case <-time.After(30 * time.Second):
		t.Fatalf("port-forward %s:%d did not start: %s", target, port, stderr.String())
		return ""
	}
}

// podName returns the name of the single running pod of a deployment.
func podName(t testing.TB, deployment string) string {
	t.Helper()
	var name string
	err := eventually(t.Context(), 3*time.Minute, func() error {
		out, err := kubectl(t.Context(), nil, "get", "pods", "-l", "app="+deployment,
			"--field-selector=status.phase=Running", "-o", "jsonpath={.items[*].metadata.name}")
		if err != nil {
			return err
		}
		names := strings.Fields(out)
		if len(names) != 1 {
			return fmt.Errorf("deployment %s has %d running pods", deployment, len(names))
		}
		name = names[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

// waitRollout waits until every replica of the deployments is ready.
func waitRollout(ctx context.Context, deployments ...string) error {
	for _, d := range deployments {
		if _, err := kubectl(ctx, nil, "rollout", "status", "deployment/"+d, "--timeout=8m"); err != nil {
			return err
		}
	}
	return nil
}

// restartPod deletes the deployment's pod and waits for the new one to run
// (not to be ready: some tests need a pod that cannot become ready).
func restartPod(t testing.TB, deployment string) string {
	t.Helper()
	old := podName(t, deployment)
	mustKubectl(t, "delete", "pod", old, "--wait=true")
	var name string
	err := eventually(t.Context(), 5*time.Minute, func() error {
		out, err := kubectl(t.Context(), nil, "get", "pods", "-l", "app="+deployment,
			"-o", "jsonpath={range .items[*]}{.metadata.name} {.status.phase}{\"\\n\"}{end}")
		if err != nil {
			return err
		}
		for line := range strings.Lines(out) {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] != old && fields[1] == "Running" {
				name = fields[0]
				return nil
			}
		}
		return errors.New("new pod not running yet")
	})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

// scale also works in t.Cleanup.
func scale(t testing.TB, deployment string, replicas int) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	mustKubectl(t, "scale", "deployment/"+deployment, fmt.Sprintf("--replicas=%d", replicas))
	if replicas == 0 {
		err := eventually(ctx, 2*time.Minute, func() error {
			out, err := kubectl(ctx, nil, "get", "pods", "-l", "app="+deployment, "-o", "name")
			if err != nil {
				return err
			}
			if strings.TrimSpace(out) != "" {
				return fmt.Errorf("pods still present: %s", out)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := waitRollout(ctx, deployment); err != nil {
		t.Fatal(err)
	}
}

func envFlag(name string) bool {
	return os.Getenv(name) == "1"
}
