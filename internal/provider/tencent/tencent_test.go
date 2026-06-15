package tencent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hoveychen/docvault/internal/provider"
)

func testProvider() *Provider {
	return New(provider.ConnDef{Type: "tencent", Key: "tencent", Label: "腾讯文档", AppID: "app_x", AppSecret: "s"})
}

func newReq(t *testing.T) (*http.Request, error) {
	t.Helper()
	return http.NewRequest(http.MethodGet, "https://docs.qq.com/openapi/x", nil)
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
		return false, 403, "permission denied", nil // not the rate-limit sentinel
	})
	if err == nil || calls != 1 {
		t.Fatalf("want immediate error without retry, got err=%v calls=%d", err, calls)
	}
}

func TestCall_RetriesOnRateLimit(t *testing.T) {
	p := testProvider()
	calls := 0
	err := p.call(context.Background(), "x", func() (bool, int, string, error) {
		calls++
		if calls < 2 {
			return false, codeRateLimited, "rate limited", nil
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

func TestAuthCodeURL(t *testing.T) {
	p := New(provider.ConnDef{Key: "tencent", Label: "腾讯文档", AppID: "app_test123", AppSecret: "s"})
	raw := p.AuthCodeURL("state-xyz", "http://localhost:8088/api/auth/tencent/callback")

	if !strings.HasPrefix(raw, oauthAuthorizeURL+"?") {
		t.Errorf("unexpected authorize base: %s", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad url: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != "app_test123" {
		t.Errorf("client_id missing/wrong: got %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://localhost:8088/api/auth/tencent/callback" {
		t.Errorf("redirect_uri wrong: %q", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type wrong: %q", q.Get("response_type"))
	}
	if q.Get("scope") == "" {
		t.Error("scope missing")
	}
	if q.Get("state") != "state-xyz" {
		t.Errorf("state wrong: %q", q.Get("state"))
	}
}

func TestExportSpecMapping(t *testing.T) {
	cases := map[string]struct{ exportType, ext string }{
		"doc":         {"doc", "docx"},
		"sheet":       {"sheet", "xlsx"},
		"slide":       {"slide", "pptx"},
		"pdf":         {"pdf", "pdf"},
		"smartcanvas": {"doc", "docx"},
	}
	for docType, want := range cases {
		got, ok := exportSpec[docType]
		if !ok {
			t.Errorf("doc type %q should be exportable", docType)
			continue
		}
		if got.exportType != want.exportType || got.ext != want.ext {
			t.Errorf("doc type %q: want %+v, got %+v", docType, want, got)
		}
	}
	if _, ok := exportSpec["folder"]; ok {
		t.Error("folder must not be exportable")
	}
}

func TestExportSpecHasContentType(t *testing.T) {
	// Every export extension we can produce must have a content type mapping,
	// otherwise the Blob ships with an empty ContentType.
	for _, spec := range exportSpec {
		if contentTypes[spec.ext] == "" {
			t.Errorf("ext %q missing content type", spec.ext)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"":        "untitled",
		"normal":  "normal",
		"a/b":     "a_b",
		"a\\b":    "a_b",
		"x:y*z?":  "x_y_z_",
		"q\"<>|w": "q____w",
		"季度报表 Q3": "季度报表 Q3",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinPath(t *testing.T) {
	cases := []struct {
		prefix, name, want string
	}{
		{"", "a", "a"},
		{"a", "b", "a/b"},
		{"a/b", "c", "a/b/c"},
	}
	for _, c := range cases {
		if got := joinPath(c.prefix, c.name); got != c.want {
			t.Errorf("joinPath(%q,%q) = %q, want %q", c.prefix, c.name, got, c.want)
		}
	}
}

func TestFileEntryIsFolder(t *testing.T) {
	if !(fileEntry{ID: "y", Type: "folder"}).isFolder() {
		t.Error("type=folder must be a folder")
	}
	if (fileEntry{ID: "x", Type: "doc"}).isFolder() {
		t.Error("type=doc must not be a folder")
	}
}

// resolveOpenID must return a cached openID without any network call.
func TestResolveOpenID_CacheHit(t *testing.T) {
	p := testProvider()
	p.openIDs.Store("at-123", "open-abc")
	got, err := p.resolveOpenID(context.Background(), "at-123")
	if err != nil || got != "open-abc" {
		t.Fatalf("want cached open-abc, got %q err=%v", got, err)
	}
}

// setAuthHeaders must set all three required headers.
func TestSetAuthHeaders(t *testing.T) {
	p := testProvider()
	req, _ := newReq(t)
	p.setAuthHeaders(req, "tok-1", "open-9")
	if req.Header.Get("Access-Token") != "tok-1" {
		t.Errorf("Access-Token wrong: %q", req.Header.Get("Access-Token"))
	}
	if req.Header.Get("Client-Id") != "app_x" {
		t.Errorf("Client-Id wrong: %q", req.Header.Get("Client-Id"))
	}
	if req.Header.Get("Open-Id") != "open-9" {
		t.Errorf("Open-Id wrong: %q", req.Header.Get("Open-Id"))
	}
}
