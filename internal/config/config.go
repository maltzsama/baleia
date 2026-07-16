package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port         string
	DatabaseURL  string
	TemporalHost string
	PolarisURL   string
	TrinoURL     string
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
	RateLimitRPS   int
	RateLimitBurst int
}

func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", "8080"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable"),
		TemporalHost: getEnv("TEMPORAL_HOST", "localhost:7233"),
		PolarisURL:   getEnv("POLARIS_URL", "http://localhost:8181"),
		TrinoURL:     getEnv("TRINO_URL", "http://localhost:8088"),
		JWTSecret:    getEnv("JWT_SECRET", "change-me-in-production"),
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
		S3UseSSL:     getEnv("S3_USE_SSL", "false") == "true",
		RateLimitRPS:   getEnvInt("RATE_LIMIT_RPS", 60),
		RateLimitBurst: getEnvInt("RATE_LIMIT_BURST", 10),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
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
