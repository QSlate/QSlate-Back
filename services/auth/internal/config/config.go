package config

import (
	"fmt"
	"time"

	common "qslate/internal/platform/config"
)

type Config struct {
	Address, DatabaseURL, PrivateKeyFile, Issuer, Audience string
	TTL                                                    time.Duration
	BcryptCost                                             int
}

func Load() (Config, error) {
	cfg := Config{Address: common.Env("HTTP_ADDR", ":8081"), PrivateKeyFile: common.Env("JWT_PRIVATE_KEY_FILE", ".secrets/auth-private.pem"), Issuer: common.Env("JWT_ISSUER", "qslate-auth"), Audience: common.Env("JWT_AUDIENCE", "qslate-api")}
	var err error
	if cfg.DatabaseURL, err = common.Required("DATABASE_URL"); err != nil {
		return cfg, err
	}
	if cfg.TTL, err = common.Duration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return cfg, err
	}
	if cfg.TTL < time.Minute || cfg.TTL > time.Hour {
		return cfg, fmt.Errorf("ACCESS_TOKEN_TTL must be between 1m and 1h")
	}
	if cfg.BcryptCost, err = common.Int("BCRYPT_COST", 12, 10, 14); err != nil {
		return cfg, err
	}
	if cfg.Issuer == "" || cfg.Audience == "" || cfg.Address == "" {
		return cfg, fmt.Errorf("HTTP_ADDR, JWT_ISSUER and JWT_AUDIENCE must not be empty")
	}
	return cfg, nil
}
