package config

import "os"

// Config holds the application configuration.
type Config struct {
	Port     string
	LogLevel string
	Env      string
}

// Load reads configuration from env and returns a Config struct.
func Load() (Config, error) {
	return Config{
		Port:     getEnv("PORT", "8080"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
		Env:      getEnv("ENV", "dev"),
	}, nil
}

// getEnv returns the value of the environment variable if it exists, otherwise it returns the fallback value.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
