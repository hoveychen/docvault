package feishu

import (
	"net/url"
	"strings"
	"testing"

	"github.com/hoveychen/docvault/internal/config"
)

// AuthCodeURL must include app_id — Feishu/Lark's authorize endpoint requires it
// to identify the app, otherwise the authorization page errors.
func TestAuthCodeURLIncludesAppID(t *testing.T) {
	p := New(config.FeishuConnection{
		Key: "feishu", Label: "Lark", AppID: "cli_test123", AppSecret: "s", Domain: "lark",
	})
	raw := p.AuthCodeURL("state-xyz", "http://localhost:8088/api/auth/feishu/callback")

	u, err := parse(t, raw)
	if err != nil {
		t.Fatalf("bad url: %v", err)
	}
	if !strings.HasPrefix(raw, "https://open.larksuite.com/open-apis/authen/v1/authorize") {
		t.Errorf("unexpected authorize base: %s", raw)
	}
	q := u.Query()
	if q.Get("app_id") != "cli_test123" {
		t.Errorf("app_id missing/wrong: got %q", q.Get("app_id"))
	}
	if q.Get("redirect_uri") != "http://localhost:8088/api/auth/feishu/callback" {
		t.Errorf("redirect_uri wrong: %q", q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-xyz" {
		t.Errorf("state wrong: %q", q.Get("state"))
	}
	if q.Get("scope") == "" {
		t.Error("scope missing")
	}
}

func parse(t *testing.T, raw string) (*url.URL, error) {
	t.Helper()
	return url.Parse(raw)
}
