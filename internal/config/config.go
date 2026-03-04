package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	TelegramBotToken  string
	TelegramMode      string // "webhook" or "polling"
	TelegramWebhookURL string
	BaseURL           string
	RedisURL          string
	StoragePath       string
	MasterKey         string // optional; enables at-rest encryption

	MaxIPAMB          int
	MaxP12MB          int
	MaxProvKB         int
	MaxCertSetsPerUser int

	WorkerConcurrency        int
	UserConcurrency          int
	JobTimeoutSigningSeconds int
	VisibilityTimeoutSeconds int
	RetentionDaysDefault     int
}

// Load reads configuration from environment variables with defaults.
func Load() (*Config, error) {
	c := &Config{
		TelegramBotToken:   os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramMode:       envOrDefault("TELEGRAM_MODE", "polling"),
		TelegramWebhookURL: os.Getenv("TELEGRAM_WEBHOOK_URL"),
		BaseURL:            os.Getenv("BASE_URL"),
		RedisURL:           envOrDefault("REDIS_URL", "redis://redis:6379/0"),
		StoragePath:        envOrDefault("STORAGE_PATH", "/storage"),
		MasterKey:          os.Getenv("MASTER_KEY"),

		MaxIPAMB:          envOrDefaultInt("MAX_IPA_MB", 500),
		MaxP12MB:          envOrDefaultInt("MAX_P12_MB", 20),
		MaxProvKB:         envOrDefaultInt("MAX_PROV_KB", 512),
		MaxCertSetsPerUser: envOrDefaultInt("MAX_CERTSETS_PER_USER", 3),

		WorkerConcurrency:        envOrDefaultInt("WORKER_CONCURRENCY", 2),
		UserConcurrency:          envOrDefaultInt("USER_CONCURRENCY", 1),
		JobTimeoutSigningSeconds: envOrDefaultInt("JOB_TIMEOUT_SIGNING_SECONDS", 900),
		VisibilityTimeoutSeconds: envOrDefaultInt("VISIBILITY_TIMEOUT_SECONDS", 600),
		RetentionDaysDefault:     envOrDefaultInt("RETENTION_DAYS_DEFAULT", 7),
	}

	if c.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if c.BaseURL == "" {
		return nil, fmt.Errorf("BASE_URL is required")
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")

	if c.TelegramMode == "webhook" && c.TelegramWebhookURL == "" {
		return nil, fmt.Errorf("TELEGRAM_WEBHOOK_URL is required when TELEGRAM_MODE=webhook")
	}

	return c, nil
}

// HasMasterKey returns true if a MASTER_KEY is configured for at-rest encryption.
func (c *Config) HasMasterKey() bool {
	return c.MasterKey != ""
}

// MaxIPABytes returns the max IPA size in bytes.
func (c *Config) MaxIPABytes() int64 {
	return int64(c.MaxIPAMB) * 1024 * 1024
}

// MaxP12Bytes returns the max P12 size in bytes.
func (c *Config) MaxP12Bytes() int64 {
	return int64(c.MaxP12MB) * 1024 * 1024
}

// MaxProvBytes returns the max provisioning profile size in bytes.
func (c *Config) MaxProvBytes() int64 {
	return int64(c.MaxProvKB) * 1024
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envOrDefaultInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}
