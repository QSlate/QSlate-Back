package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	common "qslate/services/auth/internal/platform/config"
)

type Config struct {
	Address, DatabaseURL, PrivateKeyFile, Issuer, Audience                            string
	TTL                                                                               time.Duration
	BcryptCost                                                                        int
	RefreshTTL                                                                        time.Duration
	SecureCookies                                                                     bool
	AllowedOrigins                                                                    []string
	ProvidersFile, AppURL, SMTPAddress, SMTPFrom, SMTPUsername, SMTPPassword, SMTPTLS string
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
	if cfg.RefreshTTL, err = common.Duration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return cfg, err
	}
	if cfg.RefreshTTL <= cfg.TTL || cfg.RefreshTTL > 90*24*time.Hour {
		return cfg, fmt.Errorf("REFRESH_TOKEN_TTL must exceed ACCESS_TOKEN_TTL and be at most 90d")
	}
	if cfg.SecureCookies, err = strconv.ParseBool(common.Env("COOKIE_SECURE", "true")); err != nil {
		return cfg, fmt.Errorf("COOKIE_SECURE must be true or false")
	}
	cfg.AllowedOrigins = strings.Split(common.Env("ALLOWED_ORIGINS", "http://localhost:3000"), ",")
	cfg.ProvidersFile = common.Env("OAUTH_PROVIDERS_FILE", "config/providers.json")
	cfg.AppURL = common.Env("PUBLIC_APP_URL", "http://localhost:3000")
	cfg.SMTPAddress = common.Env("SMTP_ADDR", "localhost:1025")
	cfg.SMTPFrom = common.Env("SMTP_FROM", "noreply@qslate.local")
	cfg.SMTPUsername = common.Env("SMTP_USERNAME", "")
	cfg.SMTPPassword = common.Env("SMTP_PASSWORD", "")
	cfg.SMTPTLS = common.Env("SMTP_TLS_MODE", "starttls")
	if cfg.Issuer == "" || cfg.Audience == "" || cfg.Address == "" {
		return cfg, fmt.Errorf("HTTP_ADDR, JWT_ISSUER and JWT_AUDIENCE must not be empty")
	}
	return cfg, nil
}
