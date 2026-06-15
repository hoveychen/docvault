package microsoft

import (
	"net/url"
	"strings"
	"testing"

	"github.com/hoveychen/docvault/internal/provider"
)

func testProvider(domain string) *Provider {
	return New(provider.ConnDef{
		Type: "microsoft", Key: "ms", Label: "Office 365",
		AppID: "client-abc", AppSecret: "secret", Domain: domain,
	})
}

func TestEndpoints_DefaultTenantCommon(t *testing.T) {
	p := testProvider("") // empty domain -> "common"
	if p.tenant != "common" {
		t.Fatalf("tenant = %q, want common", p.tenant)
	}
	if got := p.cfg.Endpoint.AuthURL; got != "https://login.microsoftonline.com/common/oauth2/v2.0/authorize" {
		t.Errorf("AuthURL = %q", got)
	}
	if got := p.cfg.Endpoint.TokenURL; got != "https://login.microsoftonline.com/common/oauth2/v2.0/token" {
		t.Errorf("TokenURL = %q", got)
	}
}

func TestEndpoints_ExplicitTenant(t *testing.T) {
	p := testProvider("contoso.onmicrosoft.com")
	if got := p.cfg.Endpoint.AuthURL; got != "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize" {
		t.Errorf("AuthURL = %q", got)
	}
	if got := p.cfg.Endpoint.TokenURL; got != "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/token" {
		t.Errorf("TokenURL = %q", got)
	}
}

func TestAuthCodeURL(t *testing.T) {
	p := testProvider("common")
	redirect := "http://localhost:8088/api/auth/ms/callback"
	raw := p.AuthCodeURL("state-xyz", redirect)

	if !strings.HasPrefix(raw, "https://login.microsoftonline.com/common/oauth2/v2.0/authorize") {
		t.Errorf("unexpected authorize base: %s", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad url: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != "client-abc" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != redirect {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-xyz" {
		t.Errorf("state = %q", q.Get("state"))
	}
	scope := q.Get("scope")
	for _, want := range []string{"offline_access", "User.Read", "Files.Read.All", "Sites.Read.All", "Files.ReadWrite.All"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope missing %q; got %q", want, scope)
		}
	}
}

func TestDocTypeFor(t *testing.T) {
	cases := map[string]string{
		"Report.docx":   "docx",
		"budget.XLSX":   "xlsx", // case-insensitive
		"deck.pptx":     "pptx",
		"scan.pdf":      "pdf",
		"archive.zip":   "other",
		"notes":         "other", // no extension
		"trailing.dot.": "other",
	}
	for name, want := range cases {
		if got := docTypeFor(name); got != want {
			t.Errorf("docTypeFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestExtOf(t *testing.T) {
	cases := map[string]string{
		"a.DOCX":    "docx",
		"a.b.pdf":   "pdf",
		"noext":     "",
		"trailing.": "",
	}
	for name, want := range cases {
		if got := extOf(name); got != want {
			t.Errorf("extOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestContentTypeMapping(t *testing.T) {
	if contentTypes["docx"] != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("docx content type wrong: %q", contentTypes["docx"])
	}
	if contentTypes["pdf"] != "application/pdf" {
		t.Errorf("pdf content type wrong: %q", contentTypes["pdf"])
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"a/b:c*d?.docx": "a_b_c_d_.docx",
		"":              "untitled",
		`x"<>|y.pdf`:    "x____y.pdf",
		"normal.xlsx":   "normal.xlsx",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinPath(t *testing.T) {
	if got := joinPath("", "root"); got != "root" {
		t.Errorf("joinPath empty prefix = %q", got)
	}
	if got := joinPath("a/b", "c"); got != "a/b/c" {
		t.Errorf("joinPath = %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x"); got != "x" {
		t.Errorf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty all empty = %q", got)
	}
}

func TestRetryAfter(t *testing.T) {
	if d := retryAfter("5", 0); d != 5_000_000_000 { // 5s
		t.Errorf("retryAfter(\"5\") = %v, want 5s", d)
	}
	// Empty header falls back to exponential backoff: attempt 0 -> 1s.
	if d := retryAfter("", 0); d != 1_000_000_000 {
		t.Errorf("retryAfter(\"\",0) = %v, want 1s", d)
	}
	// Garbage header also falls back to exponential.
	if d := retryAfter("bogus", 2); d != 4_000_000_000 {
		t.Errorf("retryAfter(\"bogus\",2) = %v, want 4s", d)
	}
}
