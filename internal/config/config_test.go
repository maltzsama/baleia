package config

import "testing"

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
