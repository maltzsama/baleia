// Command tlsproxy terminates TLS for the BI gateway e2e.
//
// The Trino JDBC driver, like the real BI clients, refuses HTTP Basic over
// plaintext. A real deployment terminates TLS at its Ingress; minikube has no
// such entry point for the gateway, so this proxy stands in for it: it accepts
// TLS on BI_E2E_TLS_ADDR with a self-signed certificate and forwards plain TCP
// to the gateway's port-forward (BI_E2E_TLS_TARGET). The gateway's public URL
// points at the proxy, so the URIs it rewrites are https and the JDBC driver
// follows them through the proxy.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	listen := env("BI_E2E_TLS_ADDR", "127.0.0.1:8444")
	target := env("BI_E2E_TLS_TARGET", "127.0.0.1:8443")
	cert, err := selfSigned()
	if err != nil {
		log.Fatalf("tls proxy: certificate: %v", err)
	}
	ln, err := tls.Listen("tcp", listen, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		log.Fatalf("tls proxy: listen: %v", err)
	}
	log.Printf("tls proxy %s -> %s", listen, target)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatalf("tls proxy: accept: %v", err)
		}
		go forward(conn, target)
	}
}

// forward copies one TLS connection to the plain gateway.
func forward(conn net.Conn, target string) {
	defer func() { _ = conn.Close() }()
	up, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer func() { _ = up.Close() }()
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(up, conn)
		done <- struct{}{}
	}()
	_, _ = io.Copy(conn, up)
	<-done
}

// selfSigned makes a short-lived certificate for localhost and 127.0.0.1.
func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
