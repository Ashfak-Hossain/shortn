package config

import "os"

type Config struct {
	Port     string
	LogLevel string
	Env      string
}

func Load() (Config, error) {
	return Config{
		Port:     getEnv("PORT", "8080"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
		Env:      getEnv("ENV", "dev"),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
