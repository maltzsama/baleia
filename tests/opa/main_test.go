//go:build opa

package opa

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// harness holds what the suite generated for this namespace. With
// OPA_SPIKE_REUSE=1 it is read back from the spike-harness Secret.
var harness struct {
	signingKey   *rsa.PrivateKey
	caPool       *x509.CertPool
	passwords    map[string]string
	policyDigest string
}

// The OPA configuration every sidecar uses. The data bundle must be signed
// with the harness key; the Rego comes from the verified release tarball.
const opaConfigTemplate = `services:
  baleia:
    url: http://bundles.opa-spike.svc.cluster.local/opa/
bundles:
  baleia:
    service: baleia
    resource: bundles/baleia.tar.gz
    persist: true
    polling: { min_delay_seconds: 1, max_delay_seconds: 2 }
    signing: { keyid: baleia }
  policy:
    resource: file:///policy/policy.tar.gz
keys:
  baleia:
    algorithm: RS256
    key: |
%s
persistence_directory: /var/opa
status:
  service: baleia
decision_logs:
  console: true
`

var deployments = []string{"postgres", "rustfs", "bundles", "trino-baseline", "trino-opa", "trino-opa-worker", "trino-opa-single", "trino-opa-down"}

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	err := setUp(ctx)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "opa spike setup:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func setUp(ctx context.Context) error {
	if envFlag("OPA_SPIKE_REUSE") {
		return loadHarness(ctx)
	}
	if _, err := kubectl(ctx, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=true"); err != nil {
		return err
	}
	dir := filepath.Join(repoRoot(), "deploy", "dev", "opa-spike")
	if _, err := kubectl(ctx, nil, "apply", "-f", filepath.Join(dir, "namespace.yaml")); err != nil {
		return err
	}
	if err := createHarness(ctx, dir); err != nil {
		return err
	}
	if err := waitRollout(ctx, "bundles"); err != nil {
		return err
	}
	// OPA sidecars are ready only once a data bundle is active.
	first := newBundle("r0")
	tarball, err := first.build(harness.signingKey, bundleOptions{})
	if err != nil {
		return err
	}
	script := fmt.Sprintf("mkdir -p %[1]s && cat > %[1]s/.next && mv %[1]s/.next %[2]s", filepath.Dir(bundlePath), bundlePath)
	if _, err := kubectl(ctx, tarball, "exec", "-i", "deploy/bundles", "--", "sh", "-ec", script); err != nil {
		return err
	}
	if _, err := kubectl(ctx, nil, "wait", "--for=condition=complete", "job/bucket-init", "--timeout=5m"); err != nil {
		return err
	}
	return waitRollout(ctx, deployments...)
}

// createHarness generates the TLS certificate, the password file, the
// internal shared secret, the bundle signing key and the Rego release
// tarball, stores them in the namespace and applies the harness in dir. The
// OPA configuration and the Rego release go into immutable ConfigMaps named
// by their content (ADR 0028), through an overlay of dir.
func createHarness(ctx context.Context, dir string) error {
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	certPEM, keyPEM, err := selfSigned()
	if err != nil {
		return err
	}
	passwords := map[string]string{adminUser: rand.Text(), outsider: rand.Text()}
	var passwordDB strings.Builder
	for _, user := range []string{adminUser, outsider} {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(passwords[user]), 10)
		if hashErr != nil {
			return hashErr
		}
		// Trino's file authenticator reads bcrypt as $2y$ (same algorithm as Go's $2a$).
		fmt.Fprintf(&passwordDB, "%s:%s\n", user, strings.Replace(string(hash), "$2a$", "$2y$", 1))
	}
	tarball, digest, err := policyTarball()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(tarball)
	pubDER, err := x509.MarshalPKIXPublicKey(&signingKey.PublicKey)
	if err != nil {
		return err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	var indented strings.Builder
	for line := range strings.Lines(string(pubPEM)) {
		indented.WriteString("      " + line)
	}
	tmp, err := os.MkdirTemp("", "opa-spike-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	var writeErr error
	write := func(name string, content []byte) string {
		path := filepath.Join(tmp, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			writeErr = err
		}
		return path
	}
	keyDER := x509.MarshalPKCS1PrivateKey(signingKey)
	files := map[string]string{
		"keystore":  write("keystore.pem", append(append([]byte{}, certPEM...), keyPEM...)),
		"ca":        write("ca.pem", certPEM),
		"passwords": write("password.db", []byte(passwordDB.String())),
		"signing":   write("signing.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})),
		"tarball":   write("baleia-opa-policy.tar.gz", tarball),
		"sha":       write("baleia-opa-policy.tar.gz.sha256", []byte(hex.EncodeToString(sum[:])+"\n")),
		"config":    write("config.yaml", []byte(fmt.Sprintf(opaConfigTemplate, strings.TrimRight(indented.String(), "\n")))),
	}
	if writeErr != nil {
		return writeErr
	}
	shared := rand.Text()
	steps := [][]string{
		{"create", "secret", "generic", "trino-auth",
			"--from-file=keystore.pem=" + files["keystore"],
			"--from-file=password.db=" + files["passwords"],
			"--from-literal=shared-secret=" + shared},
		// Test-only: lets OPA_SPIKE_REUSE=1 runs sign bundles and log in.
		{"create", "secret", "generic", "spike-harness",
			"--from-file=signing.pem=" + files["signing"],
			"--from-file=ca.pem=" + files["ca"],
			"--from-literal=admin-password=" + passwords[adminUser],
			"--from-literal=outsider-password=" + passwords[outsider],
			"--from-literal=policy-digest=" + digest},
	}
	for _, step := range steps {
		if _, err := kubectl(ctx, nil, step...); err != nil {
			return err
		}
	}
	// Kustomize takes a base only by a relative path.
	base, err := filepath.Rel(tmp, dir)
	if err != nil {
		return err
	}
	overlay := fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: %s
resources:
  - %s
generatorOptions:
  immutable: true
configMapGenerator:
  - name: opa-config
    files: [config.yaml]
  - name: opa-policy-release
    files: [baleia-opa-policy.tar.gz, baleia-opa-policy.tar.gz.sha256]
`, namespace, base)
	write("kustomization.yaml", []byte(overlay))
	if writeErr != nil {
		return writeErr
	}
	if _, err := kubectl(ctx, nil, "apply", "-k", tmp); err != nil {
		return err
	}
	return setHarness(signingKey, certPEM, passwords, digest)
}

func setHarness(key *rsa.PrivateKey, caPEM []byte, passwords map[string]string, digest string) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("invalid CA certificate")
	}
	harness.signingKey = key
	harness.caPool = pool
	harness.passwords = passwords
	harness.policyDigest = digest
	return nil
}

func loadHarness(ctx context.Context) error {
	read := func(key string) ([]byte, error) {
		out, err := kubectl(ctx, nil, "get", "secret", "spike-harness", "-o", "jsonpath={.data."+strings.ReplaceAll(key, ".", `\.`)+"}")
		if err != nil {
			return nil, err
		}
		return decodeBase64(out)
	}
	values := map[string][]byte{}
	for _, key := range []string{"signing.pem", "ca.pem", "admin-password", "outsider-password", "policy-digest"} {
		v, err := read(key)
		if err != nil {
			return err
		}
		values[key] = v
	}
	block, _ := pem.Decode(values["signing.pem"])
	if block == nil {
		return fmt.Errorf("spike-harness: no signing key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	return setHarness(key, values["ca.pem"], map[string]string{
		adminUser: string(values["admin-password"]),
		outsider:  string(values["outsider-password"]),
	}, string(values["policy-digest"]))
}

// selfSigned returns a certificate for localhost, which is how the suite
// reaches every coordinator (through a port-forward).
func selfSigned() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "opa-spike"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}
