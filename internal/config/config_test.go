package config

import (
	"testing"
	"time"
)

func TestValidateRejectsInsecureJWTSecret(t *testing.T) {
	for _, secret := range []string{"", DefaultJWTSecret, "short"} {
		c := &Config{JWTSecret: secret}
		if err := c.Validate(); err == nil {
			t.Fatalf("Validate accepted insecure JWT_SECRET %q", secret)
		}
	}
}

func TestValidateAcceptsCustomJWTSecret(t *testing.T) {
	c := &Config{JWTSecret: "a-long-random-value-not-the-default"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected a custom secret: %v", err)
	}
}

func TestValidateEncryptionKeys(t *testing.T) {
	const jwt = "a-long-random-value-not-the-default"
	key := "k-0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"unset is allowed (fresh install)", Config{JWTSecret: jwt}, true},
		{"valid", Config{JWTSecret: jwt, EncryptionKey: key}, true},
		{"valid with previous", Config{JWTSecret: jwt, EncryptionKey: key, EncryptionKeyPrevious: key + "-old"}, true},
		{"too short", Config{JWTSecret: jwt, EncryptionKey: "short"}, false},
		{"equals the JWT secret", Config{JWTSecret: key, EncryptionKey: key}, false},
		{"previous without current", Config{JWTSecret: jwt, EncryptionKeyPrevious: key}, false},
		{"previous too short", Config{JWTSecret: jwt, EncryptionKey: key, EncryptionKeyPrevious: "short"}, false},
	}
	for _, tc := range cases {
		err := tc.cfg.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestMetadataSyncEvery(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{0: DefaultMetadataSyncInterval, 30 * time.Minute: 30 * time.Minute} {
		if got := (&Config{MetadataSyncInterval: in}).MetadataSyncEvery(); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	const jwt = "a-long-random-value-not-the-default"
	for _, bad := range []time.Duration{-time.Minute, time.Minute} {
		if err := (&Config{JWTSecret: jwt, MetadataSyncInterval: bad}).Validate(); err == nil {
			t.Errorf("Validate accepted interval %s", bad)
		}
	}
}
