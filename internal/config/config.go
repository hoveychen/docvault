// Package config loads docvault runtime configuration from environment variables
// (optionally seeded from a .env file). All knobs are prefixed DOCVAULT_.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr  string
	PublicURL string // e.g. http://localhost:8080, used to build OAuth redirect URIs

	DatabaseURL string

	S3 S3Config

	TokenEncKey string // base64, 32 bytes, for AES-256-GCM of provider tokens
	JWTSecret   string

	// SyncInterval enables scheduled background sync: every linked account whose
	// last successful sync is older than this gets auto-enqueued. Zero = disabled
	// (sync is on-demand only). Parsed from DOCVAULT_SYNC_INTERVAL (e.g. "6h").
	SyncInterval time.Duration

	// Connections is one entry per org connection across all provider types. Each
	// becomes its own provider keyed by Key. Used only to seed the DB on first run;
	// thereafter connections are managed via the admin UI.
	Connections []ProviderConnection
}

// ProviderConnection is one org connection for any provider type, parsed from env
// to seed the DB on first run. Type selects the provider implementation (feishu,
// google, microsoft, tencent); AppID/AppSecret are the OAuth client credential;
// Domain carries a type-specific variant (feishu/lark, or the Entra tenant).
type ProviderConnection struct {
	Type      string `json:"provider_type"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	Domain    string `json:"domain"`
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

	if raw := strings.TrimSpace(os.Getenv("DOCVAULT_SYNC_INTERVAL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse DOCVAULT_SYNC_INTERVAL: %w", err)
		}
		c.SyncInterval = d
	}

	conns, err := loadConnections()
	if err != nil {
		return nil, err
	}
	c.Connections = conns

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// loadConnections builds the unified connection list seeded into the DB on first
// run: the legacy Feishu env (DOCVAULT_FEISHU_*) as feishu-typed connections, plus
// any in DOCVAULT_PROVIDER_CONNECTIONS (a JSON array carrying provider_type, for
// google/microsoft/tencent or additional feishu orgs). Keys must be unique across
// the whole set.
func loadConnections() ([]ProviderConnection, error) {
	var conns []ProviderConnection

	feishu, err := loadFeishuConnections()
	if err != nil {
		return nil, err
	}
	for _, f := range feishu {
		conns = append(conns, ProviderConnection{
			Type: "feishu", Key: f.Key, Label: f.Label, AppID: f.AppID, AppSecret: f.AppSecret, Domain: f.Domain,
		})
	}

	if raw := strings.TrimSpace(os.Getenv("DOCVAULT_PROVIDER_CONNECTIONS")); raw != "" {
		var extra []ProviderConnection
		if err := json.Unmarshal([]byte(raw), &extra); err != nil {
			return nil, fmt.Errorf("parse DOCVAULT_PROVIDER_CONNECTIONS: %w", err)
		}
		conns = append(conns, extra...)
	}

	seen := map[string]bool{}
	for i := range conns {
		c := &conns[i]
		if c.Type == "" {
			c.Type = "feishu"
		}
		if c.Key == "" || c.AppID == "" || c.AppSecret == "" {
			return nil, fmt.Errorf("provider connection %d (type %q) missing key/app_id/app_secret", i, c.Type)
		}
		if c.Label == "" {
			c.Label = c.Key
		}
		if seen[c.Key] {
			return nil, fmt.Errorf("duplicate connection key %q", c.Key)
		}
		seen[c.Key] = true
	}
	return conns, nil
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

// Configured reports whether at least one provider org connection is configured.
func (c *Config) Configured() bool {
	return len(c.Connections) > 0
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
