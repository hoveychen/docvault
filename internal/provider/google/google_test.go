package google

import (
	"net/url"
	"strings"
	"testing"

	"github.com/hoveychen/docvault/internal/provider"
)

func testProvider() *Provider {
	return New(provider.ConnDef{
		Type:      "google",
		Key:       "google",
		Label:     "Google Workspace",
		AppID:     "client-id-123.apps.googleusercontent.com",
		AppSecret: "secret",
	})
}

// AuthCodeURL must point at Google's OAuth endpoint and carry client_id,
// redirect_uri, state, the full drive scope, and offline access so a refresh token
// is returned.
func TestAuthCodeURL(t *testing.T) {
	p := testProvider()
	redirect := "http://localhost:8088/api/auth/google/callback"
	raw := p.AuthCodeURL("state-xyz", redirect)

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad url: %v", err)
	}
	if !strings.HasPrefix(raw, "https://accounts.google.com/o/oauth2/auth") {
		t.Errorf("unexpected authorize base: %s", raw)
	}
	q := u.Query()
	if q.Get("client_id") != "client-id-123.apps.googleusercontent.com" {
		t.Errorf("client_id missing/wrong: got %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != redirect {
		t.Errorf("redirect_uri wrong: %q", q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-xyz" {
		t.Errorf("state wrong: %q", q.Get("state"))
	}
	if q.Get("scope") != driveScope {
		t.Errorf("scope wrong: got %q want %q", q.Get("scope"), driveScope)
	}
	// offline access (access_type=offline) is required to receive a refresh token.
	if q.Get("access_type") != "offline" {
		t.Errorf("access_type must be offline; got %q", q.Get("access_type"))
	}
	// ApprovalForce -> prompt=consent (or approval_prompt=force on the legacy
	// endpoint) so a refresh token is returned even on re-consent.
	if q.Get("prompt") != "consent" && q.Get("approval_prompt") != "force" {
		t.Errorf("expected forced consent for refresh token; got prompt=%q approval_prompt=%q",
			q.Get("prompt"), q.Get("approval_prompt"))
	}
}

// withRedirect must not mutate the shared base config (concurrent flows with
// different callback URLs must not race on RedirectURL).
func TestWithRedirectDoesNotMutateBase(t *testing.T) {
	p := testProvider()
	if p.cfg.RedirectURL != "" {
		t.Fatalf("base config should start with empty RedirectURL, got %q", p.cfg.RedirectURL)
	}
	c := p.withRedirect("http://a/cb")
	if c.RedirectURL != "http://a/cb" {
		t.Errorf("withRedirect did not set RedirectURL: %q", c.RedirectURL)
	}
	if p.cfg.RedirectURL != "" {
		t.Errorf("base config RedirectURL was mutated to %q", p.cfg.RedirectURL)
	}
}

// nativeExports must map the four Google-native types to the documented office
// formats; other (binary) mime types must NOT be present so Export downloads them
// raw instead.
func TestNativeExportMapping(t *testing.T) {
	cases := []struct {
		mime    string
		ext     string
		ctType  string
	}{
		{"application/vnd.google-apps.document", "docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"application/vnd.google-apps.spreadsheet", "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"application/vnd.google-apps.presentation", "pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"application/vnd.google-apps.drawing", "pdf", "application/pdf"},
	}
	for _, c := range cases {
		got, ok := nativeExports[c.mime]
		if !ok {
			t.Errorf("missing native export mapping for %q", c.mime)
			continue
		}
		if got.ext != c.ext {
			t.Errorf("%q: ext = %q, want %q", c.mime, got.ext, c.ext)
		}
		if got.contentType != c.ctType {
			t.Errorf("%q: contentType = %q, want %q", c.mime, got.contentType, c.ctType)
		}
	}
	// A binary mime type must not be treated as a native export.
	if _, ok := nativeExports["application/pdf"]; ok {
		t.Errorf("application/pdf must not be a native export (it is a binary download)")
	}
	if _, ok := nativeExports["image/png"]; ok {
		t.Errorf("image/png must not be a native export")
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"":                 "untitled",
		"plain":            "plain",
		"a/b\\c:d*e?f":     "a_b_c_d_e_f",
		"q?\"<>|name":      "q_____name",
		"report 2026.xlsx": "report 2026.xlsx",
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
		{"", "root", "root"},
		{"a", "b", "a/b"},
		{"a/b", "c", "a/b/c"},
	}
	for _, c := range cases {
		if got := joinPath(c.prefix, c.name); got != c.want {
			t.Errorf("joinPath(%q,%q) = %q, want %q", c.prefix, c.name, got, c.want)
		}
	}
}

func TestExtOf(t *testing.T) {
	cases := map[string]string{
		"file.pdf":     "pdf",
		"a.b.tar.gz":   "gz",
		"noext":        "",
		"trailingdot.": "",
		".hidden":      "hidden",
	}
	for in, want := range cases {
		if got := extOf(in); got != want {
			t.Errorf("extOf(%q) = %q, want %q", in, got, want)
		}
	}
}
