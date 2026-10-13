package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// DefaultJWTSecret is the insecure placeholder shipped for local development.
// It signs JWTs and derives the credential-encryption key, so ValidateSecret
// rejects it.
const DefaultJWTSecret = "change-me-in-production"

// MinJWTSecretLen is the minimum accepted length for JWT_SECRET. Short secrets
// are guessable and the key is reused for credential encryption.
const MinJWTSecretLen = 16

type Config struct {
	Port         string
	DatabaseURL  string
	TemporalHost string
	PolarisURL   string
	TrinoURL     string
	// TrinoDBPassword is the password for the dedicated baleia_trino role, from
	// the same env var the Trino plugin reads. Empty means "leave it unchanged".
	TrinoDBPassword string
	JWTSecret    string
	PublicURL    string
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	S3Endpoint   string
	S3AccessKey  string
	S3SecretKey  string
	S3Bucket     string
	S3UseSSL     bool
	// StatementTimeout bounds a single statement execution. It is used as the
	// activity StartToCloseTimeout and sent to Trino as the session property
	// query_max_execution_time so the cluster enforces it too.
	StatementTimeout time.Duration
	// ResultMaxRows / ResultMaxBytes cap one statement's result. When a cap is
	// hit the remaining query is killed and the result is marked truncated.
	ResultMaxRows  int64
	ResultMaxBytes int64
	// SchedulesEnabled drives whether the worker ensures its recurring Temporal
	// Schedules at boot. Disabled for tests and local dev that lack Temporal.
	SchedulesEnabled bool
	RateLimitRPS     int
	RateLimitBurst   int
}

func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", "8080"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable"),
		TemporalHost: getEnv("TEMPORAL_HOST", "localhost:7233"),
		PolarisURL:   getEnv("POLARIS_URL", "http://localhost:8181"),
		TrinoURL:     getEnv("TRINO_URL", "http://localhost:8088"),
		TrinoDBPassword: getEnv("BALEIA_TRINO_DB_PASSWORD", ""),
		JWTSecret:    getEnv("JWT_SECRET", DefaultJWTSecret),
		PublicURL:    getEnv("PUBLIC_URL", "http://localhost:8080"),
		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnv("SMTP_PORT", ""),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		S3Endpoint:   getEnv("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey:  getEnv("S3_ACCESS_KEY", "rustfsadmin"),
		S3SecretKey:  getEnv("S3_SECRET_KEY", "rustfsadmin"),
		S3Bucket:     getEnv("S3_BUCKET", "baleia"),
		S3UseSSL:         getEnv("S3_USE_SSL", "false") == "true",
		StatementTimeout: getEnvDuration("BALEIA_STATEMENT_TIMEOUT", 30*time.Minute),
		ResultMaxRows:    getEnvInt64("BALEIA_RESULT_MAX_ROWS", 1000000),
		ResultMaxBytes:   getEnvInt64("BALEIA_RESULT_MAX_BYTES", 536870912),
		SchedulesEnabled: getEnv("BALEIA_SCHEDULES_ENABLED", "true") == "true",
		RateLimitRPS:     getEnvInt("RATE_LIMIT_RPS", 60),
		RateLimitBurst:   getEnvInt("RATE_LIMIT_BURST", 10),
	}
}

// Validate rejects configuration that must not reach production: a missing or
// default JWT_SECRET would let anyone with the database recover stored
// credentials and forge tokens.
// ValidateSecret reports why s is unacceptable as JWT_SECRET, or nil when it is
// safe for signing and credential encryption.
func ValidateSecret(s string) error {
	if s == "" || s == DefaultJWTSecret {
		return errors.New("JWT_SECRET must be set to a non-default, high-entropy value")
	}
	if len(s) < MinJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", MinJWTSecretLen)
	}
	return nil
}

// Validate rejects configuration that must not reach production.
func (c *Config) Validate() error {
	if err := ValidateSecret(c.JWTSecret); err != nil {
		return err
	}
	if c.StatementTimeout != 0 && c.StatementTimeout < time.Minute {
		return fmt.Errorf("BALEIA_STATEMENT_TIMEOUT must be at least 1m (got %s)", c.StatementTimeout)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
