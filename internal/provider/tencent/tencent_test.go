package tencent

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/hoveychen/docvault/internal/provider"
)

func testProvider() *Provider {
	return New(provider.ConnDef{Type: "tencent", Key: "tencent", Label: "腾讯文档", AppID: "app_x", AppSecret: "s"})
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

func TestExportableMapping(t *testing.T) {
	cases := map[string]string{
		"doc":   "docx",
		"docx":  "docx",
		"sheet": "xlsx",
		"slide": "pptx",
		"pptx":  "pptx",
		"pdf":   "pdf",
	}
	for docType, wantExt := range cases {
		got, ok := exportable[docType]
		if !ok {
			t.Errorf("doc type %q should be exportable", docType)
			continue
		}
		if got != wantExt {
			t.Errorf("doc type %q: want ext %q, got %q", docType, wantExt, got)
		}
	}
	if _, ok := exportable["folder"]; ok {
		t.Error("folder must not be exportable")
	}
}

func TestExportableHasContentType(t *testing.T) {
	// Every export extension we can produce must have a content type mapping,
	// otherwise the Blob ships with an empty ContentType.
	for _, ext := range exportable {
		if ext == "pdf" || ext == "docx" || ext == "xlsx" || ext == "pptx" {
			if contentTypes[ext] == "" {
				t.Errorf("ext %q missing content type", ext)
			}
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

func TestFileEntryAccessors(t *testing.T) {
	f := fileEntry{ID: "id1", Title: "T", Type: "doc"}
	if f.id() != "id1" || f.title() != "T" || f.docType() != "doc" {
		t.Errorf("accessors wrong: %q %q %q", f.id(), f.title(), f.docType())
	}
	// alternate spellings + folder detection
	f2 := fileEntry{FileID: "fid", Name: "N", FileType: "sheet"}
	if f2.id() != "fid" || f2.title() != "N" || f2.docType() != "sheet" {
		t.Errorf("alt accessors wrong: %q %q %q", f2.id(), f2.title(), f2.docType())
	}
	f3 := fileEntry{ID: "x", IsFolder: true}
	if f3.docType() != "folder" {
		t.Errorf("folder flag not honored: %q", f3.docType())
	}
	f4 := fileEntry{ID: "y", Type: "folder"}
	if f4.docType() != "folder" {
		t.Errorf("folder type not honored: %q", f4.docType())
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "", "z") != "z" {
		t.Error("want z")
	}
	if firstNonEmpty("a", "b") != "a" {
		t.Error("want a")
	}
	if firstNonEmpty("", "") != "" {
		t.Error("want empty")
	}
}

func TestBizCode(t *testing.T) {
	if bizCode(5, 0) != 5 {
		t.Error("ret should win")
	}
	if bizCode(0, 7) != 7 {
		t.Error("code fallback")
	}
	if bizCode(0, 0) != 0 {
		t.Error("zero")
	}
}
