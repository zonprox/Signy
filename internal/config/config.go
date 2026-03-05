package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all application configuration.
type Config struct {
	TelegramBotToken string
	BaseURL          string // optional; auto-detected by entrypoint if empty
	RedisURL         string
	StoragePath      string
	MasterKey        string
	AdminPassword    string
	AppPort          string

	MaxIPAMB           int
	MaxP12MB           int
	MaxProvKB          int
	MaxCertSetsPerUser int

	WorkerConcurrency        int
	UserConcurrency          int
	JobTimeoutSigningSeconds int
	VisibilityTimeoutSeconds int
	RetentionDaysDefault     int
}

// Load reads configuration from environment variables.
// Docker Secrets at /run/secrets/<name> take precedence over env vars.
func Load() (*Config, error) {
	c := &Config{
		TelegramBotToken: readSecret("telegram_bot_token", "TELEGRAM_BOT_TOKEN"),
		BaseURL:          strings.TrimRight(os.Getenv("BASE_URL"), "/"),
		RedisURL:         envOrDefault("REDIS_URL", "redis://redis:6379/0"),
		StoragePath:      envOrDefault("STORAGE_PATH", "/storage"),
		MasterKey:        readSecret("master_key", "MASTER_KEY"),
		AdminPassword:    os.Getenv("ADMIN_PASSWORD"),
		AppPort:          envOrDefault("APP_PORT", "7890"),

		MaxIPAMB:           envOrDefaultInt("MAX_IPA_MB", 500),
		MaxP12MB:           envOrDefaultInt("MAX_P12_MB", 20),
		MaxProvKB:          envOrDefaultInt("MAX_PROV_KB", 512),
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

	return c, nil
}

// HasMasterKey returns true if at-rest encryption is enabled.
func (c *Config) HasMasterKey() bool { return c.MasterKey != "" }

// MaxIPABytes returns the max IPA size in bytes.
func (c *Config) MaxIPABytes() int64 { return int64(c.MaxIPAMB) * 1024 * 1024 }

// MaxP12Bytes returns the max P12 size in bytes.
func (c *Config) MaxP12Bytes() int64 { return int64(c.MaxP12MB) * 1024 * 1024 }

// MaxProvBytes returns the max provisioning profile size in bytes.
func (c *Config) MaxProvBytes() int64 { return int64(c.MaxProvKB) * 1024 }

// readSecret reads a Docker Secret file, falling back to an env var.
func readSecret(secretName, envFallback string) string {
	data, err := os.ReadFile(filepath.Join("/run/secrets", secretName))
	if err == nil {
		if v := strings.TrimRight(string(data), "\n\r"); v != "" {
			return v
		}
	}
	return os.Getenv(envFallback)
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
