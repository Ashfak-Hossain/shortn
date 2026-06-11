// Package config loads service configuration from environment variables into a typed Config struct.
package config

import "os"

// Config holds everything the service needs to boot.
type Config struct {
	Port        string // PORT, default 8080
	LogLevel    string // LOG_LEVEL: debug|info|warn|error, default info
	Env         string // ENV: dev|staging|prod, default dev
	DatabaseURL string // DATABASE_URL, Postgres DSN
}

// Load reads config from env, applying defaults.
func Load() (Config, error) {
	return Config{
		Port:        getEnv("PORT", "8080"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		Env:         getEnv("ENV", "dev"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://dev:dev@localhost:5432/shortn?sslmode=disable"),
	}, nil
}

// getEnv returns the env value, or fallback if unset/empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
