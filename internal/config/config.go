// Package config loads service configuration from environment variables
// into a typed Config struct.
package config

import "os"

// Config holds everything the service needs to boot.
type Config struct {
	Port          string // PORT, default 8080
	LogLevel      string // LOG_LEVEL: debug|info|warn|error, default info
	Env           string // ENV: dev|staging|prod, default dev
	DatabaseURL   string // DATABASE_URL, Postgres DSN
	RedisURL      string // REDIS_URL, Redis DSN
	WorkerID      string // WORKER_ID, required; no default
	InstanceID    string // INSTANCE_ID, defaults to hostname
	SqidsAlphabet string // SQIDS_ALPHABET — shuffled base62 alphabet; must never change after codes are issued

}

// Load reads the configuration from the environment variables, applying
// appropriate fallback defaults where necessary.
func Load() (Config, error) {
	return Config{
		Port:          getEnv("PORT", "8080"),
		LogLevel:      getEnv("LOG_LEVEL", "info"),
		Env:           getEnv("ENV", "dev"),
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://dev:dev@localhost:5432/shortn?sslmode=disable"),
		RedisURL:      getEnv("REDIS_URL", "redis://localhost:6379/0"),
		WorkerID:      os.Getenv("WORKER_ID"),
		InstanceID:    getEnvOrHostname("INSTANCE_ID"),
		SqidsAlphabet: getEnv("SQIDS_ALPHABET", "0aA1bB2cC3dD4eE5fF6gG7hH8iI9jJkKlLmMnNoOpPqQrRsStTuUvVwWxXyYzZ"),
	}, nil
}

// getEnv returns the environment variable value associated with the key,
// or the fallback string if it is unset or empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvOrHostname returns the environment variable value associated with the key,
// or the system hostname if the environment variable is unset or empty.
func getEnvOrHostname(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	h, _ := os.Hostname()
	return h
}
