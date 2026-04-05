package config

import "os"

type Config struct {
	Port         string
	DatabaseURL  string
	TemporalHost string
	PolarisURL   string
	TrinoURL     string
	JWTSecret    string
	PublicURL    string
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
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
