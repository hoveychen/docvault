package feishu

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/hoveychen/docvault/internal/config"
)

func testProvider() *Provider {
	return New(config.FeishuConnection{Key: "feishu", Label: "Lark", AppID: "cli_x", AppSecret: "s", Domain: "lark"})
}

func TestCall_SuccessNoRetry(t *testing.T) {
	p := testProvider()
	calls := 0
	err := p.call(context.Background(), "x", func() (bool, int, string, error) {
		calls++
		return true, 0, "", nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("want success in 1 call, got err=%v calls=%d", err, calls)
	}
}

func TestCall_NonRateLimitNoRetry(t *testing.T) {
	p := testProvider()
	calls := 0
	err := p.call(context.Background(), "x", func() (bool, int, string, error) {
		calls++
		return false, 1254005, "permission denied", nil // not the frequency-limit code
	})
	if err == nil || calls != 1 {
		t.Fatalf("want immediate error without retry, got err=%v calls=%d", err, calls)
	}
}

func TestCall_RetriesOnFrequencyLimit(t *testing.T) {
	p := testProvider()
	calls := 0
	err := p.call(context.Background(), "x", func() (bool, int, string, error) {
		calls++
		if calls < 2 {
			return false, codeFrequencyLimit, "request trigger frequency limit", nil
		}
		return true, 0, "", nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("want success after one backoff, got err=%v calls=%d", err, calls)
	}
}

func TestCall_TransportErrorReturned(t *testing.T) {
	p := testProvider()
	want := errors.New("boom")
	err := p.call(context.Background(), "x", func() (bool, int, string, error) {
		return false, 0, "", want
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want transport error surfaced, got %v", err)
	}
}

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
	scope := q.Get("scope")
	if scope == "" {
		t.Error("scope missing")
	}
	// docs:document:readonly is not a valid Lark scope (error 20043); make sure we
	// don't request it.
	if strings.Contains(scope, "docs:document") {
		t.Errorf("must not request invalid scope docs:document; got %q", scope)
	}
	for _, want := range []string{"drive:drive:readonly", "wiki:wiki:readonly"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope missing %q; got %q", want, scope)
		}
	}
}

func parse(t *testing.T, raw string) (*url.URL, error) {
	t.Helper()
	return url.Parse(raw)
}
