// Package config loads docvault runtime configuration from environment variables
// (optionally seeded from a .env file). All knobs are prefixed DOCVAULT_.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr  string
	PublicURL string // e.g. http://localhost:8080, used to build OAuth redirect URIs

	DatabaseURL string

	S3 S3Config

	TokenEncKey string // base64, 32 bytes, for AES-256-GCM of provider tokens
	JWTSecret   string

	Feishu FeishuConfig
}

type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string
}

type FeishuConfig struct {
	AppID     string
	AppSecret string
	Domain    string // "feishu" or "lark"
}

// Load reads configuration. It first loads .env if present (non-fatal when absent),
// then reads environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load() // best effort; real env wins / .env is dev convenience

	c := &Config{
		HTTPAddr:    envOr("DOCVAULT_HTTP_ADDR", ":8080"),
		PublicURL:   strings.TrimRight(envOr("DOCVAULT_PUBLIC_URL", "http://localhost:8080"), "/"),
		DatabaseURL: os.Getenv("DOCVAULT_DATABASE_URL"),
		S3: S3Config{
			Endpoint:  envOr("DOCVAULT_S3_ENDPOINT", "localhost:9000"),
			AccessKey: envOr("DOCVAULT_S3_ACCESS_KEY", "minioadmin"),
			SecretKey: envOr("DOCVAULT_S3_SECRET_KEY", "minioadmin"),
			Bucket:    envOr("DOCVAULT_S3_BUCKET", "docvault"),
			UseSSL:    envBool("DOCVAULT_S3_USE_SSL", false),
			Region:    envOr("DOCVAULT_S3_REGION", "us-east-1"),
		},
		TokenEncKey: os.Getenv("DOCVAULT_TOKEN_ENC_KEY"),
		JWTSecret:   os.Getenv("DOCVAULT_JWT_SECRET"),
		Feishu: FeishuConfig{
			AppID:     os.Getenv("DOCVAULT_FEISHU_APP_ID"),
			AppSecret: os.Getenv("DOCVAULT_FEISHU_APP_SECRET"),
			Domain:    envOr("DOCVAULT_FEISHU_DOMAIN", "feishu"),
		},
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DOCVAULT_DATABASE_URL")
	}
	if c.TokenEncKey == "" {
		missing = append(missing, "DOCVAULT_TOKEN_ENC_KEY")
	}
	if c.JWTSecret == "" {
		missing = append(missing, "DOCVAULT_JWT_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

// FeishuConfigured reports whether the Feishu provider has credentials.
func (c *Config) FeishuConfigured() bool {
	return c.Feishu.AppID != "" && c.Feishu.AppSecret != ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
