// Package config contains infrastructure configuration helpers, never business rules.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func Env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func Duration(key string, fallback time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(Env(key, fallback.String()))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func Int(key string, fallback, min, max int) (int, error) {
	value, err := strconv.Atoi(Env(key, strconv.Itoa(fallback)))
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return value, nil
}

func Required(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}
