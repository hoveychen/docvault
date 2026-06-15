// Package config loads docvault runtime configuration from environment variables
// (optionally seeded from a .env file). All knobs are prefixed DOCVAULT_.
package config

import (
	"encoding/json"
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

	// FeishuConnections is one entry per Feishu/Lark org (self-built app). Each
	// becomes its own provider keyed by Key.
	FeishuConnections []FeishuConnection
}

type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string
}

// FeishuConnection is one org's self-built app. Key is the stable provider key
// used in routes and stored on documents/accounts (e.g. "feishu" or "org-acme").
type FeishuConnection struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	Domain    string `json:"domain"` // "feishu" or "lark"
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
	}

	conns, err := loadFeishuConnections()
	if err != nil {
		return nil, err
	}
	c.FeishuConnections = conns

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// loadFeishuConnections reads either DOCVAULT_FEISHU_CONNECTIONS (a JSON array of
// connections — one per org) or the legacy single DOCVAULT_FEISHU_APP_ID/SECRET
// pair (treated as one connection keyed "feishu"). Keys must be unique.
func loadFeishuConnections() ([]FeishuConnection, error) {
	var conns []FeishuConnection

	if raw := strings.TrimSpace(os.Getenv("DOCVAULT_FEISHU_CONNECTIONS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &conns); err != nil {
			return nil, fmt.Errorf("parse DOCVAULT_FEISHU_CONNECTIONS: %w", err)
		}
	} else if id := os.Getenv("DOCVAULT_FEISHU_APP_ID"); id != "" {
		domain := envOr("DOCVAULT_FEISHU_DOMAIN", "feishu")
		conns = append(conns, FeishuConnection{
			Key:       "feishu",
			Label:     defaultLabel(domain),
			AppID:     id,
			AppSecret: os.Getenv("DOCVAULT_FEISHU_APP_SECRET"),
			Domain:    domain,
		})
	}

	seen := map[string]bool{}
	for i := range conns {
		conn := &conns[i]
		if conn.Key == "" || conn.AppID == "" || conn.AppSecret == "" {
			return nil, fmt.Errorf("feishu connection %d missing key/app_id/app_secret", i)
		}
		if conn.Domain == "" {
			conn.Domain = "feishu"
		}
		if conn.Label == "" {
			conn.Label = defaultLabel(conn.Domain)
		}
		if seen[conn.Key] {
			return nil, fmt.Errorf("duplicate feishu connection key %q", conn.Key)
		}
		seen[conn.Key] = true
	}
	return conns, nil
}

func defaultLabel(domain string) string {
	if strings.EqualFold(domain, "lark") {
		return "Lark"
	}
	return "飞书"
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

// FeishuConfigured reports whether at least one Feishu/Lark org is configured.
func (c *Config) FeishuConfigured() bool {
	return len(c.FeishuConnections) > 0
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
