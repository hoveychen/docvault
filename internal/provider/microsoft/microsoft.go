// Package microsoft implements provider.Provider for Office 365 / Microsoft 365
// using the Microsoft Graph REST API directly over net/http + golang.org/x/oauth2
// (the official microsoftgraph/msgraph-sdk-go is intentionally avoided — it pulls
// in a very large dependency tree). Auth is per-user (delegated OAuth2 with
// offline_access for refresh tokens), mirroring the Feishu provider's design.
package microsoft

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"github.com/hoveychen/docvault/internal/provider"
)

const (
	graphBaseURL  = "https://graph.microsoft.com/v1.0"
	apiRatePerSec = 8 // conservative steady-state request rate
	apiBurst      = 8
	maxAPIRetries = 6
)

// defaultTenant is used when no Entra tenant is configured. "common" lets both
// work and personal Microsoft accounts sign in.
const defaultTenant = "common"

// Delegated scopes. offline_access yields a refresh token; Files.ReadWrite.All is
// required so Delete works (matching feishu's writable-scope choice).
var scopes = []string{
	"offline_access",
	"User.Read",
	"Files.Read.All",
	"Sites.Read.All",
	"Files.ReadWrite.All",
}

// contentTypes maps a file extension (no dot) to its MIME type. OneDrive/SharePoint
// store Office documents as native OOXML, so no conversion is needed on export.
var contentTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"pdf":  "application/pdf",
}

// docTypeFor classifies a filename's extension into a coarse doc type used by the
// sync engine. Unknown extensions fall back to "other".
func docTypeFor(filename string) string {
	switch extOf(filename) {
	case "docx":
		return "docx"
	case "xlsx":
		return "xlsx"
	case "pptx":
		return "pptx"
	case "pdf":
		return "pdf"
	default:
		return "other"
	}
}

// extOf returns the lowercased extension (without dot) of a filename, or "".
func extOf(filename string) string {
	if i := strings.LastIndex(filename, "."); i >= 0 && i < len(filename)-1 {
		return strings.ToLower(filename[i+1:])
	}
	return ""
}

// Provider is one Microsoft 365 org connection (one Entra app registration).
type Provider struct {
	key     string
	label   string
	tenant  string
	cfg     *oauth2.Config
	http    *http.Client
	limiter *rate.Limiter
}

// register the microsoft factory so provider.Build(ConnDef{Type:"microsoft", …})
// works once this package is imported for its side effects (see internal/app).
func init() {
	provider.RegisterFactory("microsoft", func(def provider.ConnDef) (provider.Provider, error) {
		return New(def), nil
	})
}

// authURL / tokenURL build the Azure AD v2.0 endpoints for a tenant.
func authURL(tenant string) string {
	return fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenant)
}
func tokenURL(tenant string) string {
	return fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)
}

// New builds a provider for one org connection. ConnDef.Domain carries the Entra
// tenant (default "common"); AppID/AppSecret are the Entra app's client id/secret.
func New(def provider.ConnDef) *Provider {
	tenant := strings.TrimSpace(def.Domain)
	if tenant == "" {
		tenant = defaultTenant
	}
	cfg := &oauth2.Config{
		ClientID:     def.AppID,
		ClientSecret: def.AppSecret,
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL(tenant),
			TokenURL: tokenURL(tenant),
		},
	}
	return &Provider{
		key:     def.Key,
		label:   def.Label,
		tenant:  tenant,
		cfg:     cfg,
		http:    &http.Client{Timeout: 60 * time.Second}, // default redirect policy follows 302s
		limiter: rate.NewLimiter(apiRatePerSec, apiBurst),
	}
}

func (p *Provider) Key() string   { return p.key }
func (p *Provider) Label() string { return p.label }

// AuthCodeURL builds the OAuth authorization URL. The redirectURI must be passed
// per-call (it isn't fixed on the oauth2.Config), so it's set as an auth-code
// option. Azure returns refresh tokens because offline_access is in scope.
func (p *Provider) AuthCodeURL(state, redirectURI string) string {
	return p.cfg.AuthCodeURL(state, oauth2.SetAuthURLParam("redirect_uri", redirectURI))
}

// Exchange swaps an authorization code for tokens, then fetches the authorizing
// user's identity from Graph /me.
func (p *Provider) Exchange(ctx context.Context, code, redirectURI string) (*provider.Token, *provider.Identity, error) {
	tok, err := p.cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("redirect_uri", redirectURI))
	if err != nil {
		return nil, nil, fmt.Errorf("microsoft exchange: %w", err)
	}
	pt := tokenFrom(tok)

	var me struct {
		ID                string `json:"id"`
		DisplayName       string `json:"displayName"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := p.doJSON(ctx, pt.AccessToken, http.MethodGet, graphBaseURL+"/me", &me); err != nil {
		return nil, nil, fmt.Errorf("microsoft /me: %w", err)
	}
	identity := &provider.Identity{
		ExternalUserID: me.ID,
		DisplayName:    me.DisplayName,
		Email:          firstNonEmpty(me.Mail, me.UserPrincipalName),
	}
	return pt, identity, nil
}

// Refresh obtains a fresh access token from a refresh token. Azure rotates refresh
// tokens, so we propagate whatever the token source returns.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*provider.Token, error) {
	ts := p.cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("microsoft refresh: %w", err)
	}
	return tokenFrom(tok), nil
}

// graphChild is one item from a /children listing. A folder has a non-nil Folder
// facet; a file has a non-nil File facet.
type graphChild struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Folder    *struct{} `json:"folder"`
	File      *struct{} `json:"file"`
	CreatedBy *struct {
		User *struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"createdBy"`
}

type childrenPage struct {
	Value    []graphChild `json:"value"`
	NextLink string       `json:"@odata.nextLink"`
}

// List walks the signed-in user's OneDrive recursively via Graph REST.
//
// SharePoint sites enumeration is intentionally NOT implemented here: the
// orchestrator's spec marks it optional/best-effort, and getting it right
// (per-site drives, permission edge cases) risks aborting the OneDrive walk. So
// this provider is OneDrive-only for now — see the package note above.
func (p *Provider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	var items []provider.Item
	if err := p.walk(ctx, tok.AccessToken, graphBaseURL+"/me/drive/root/children", "", &items); err != nil {
		return nil, err
	}
	return items, nil
}

// walk paginates one folder's children, recording files and recursing into
// sub-folders. startURL is the first page's URL; pathPrefix is the parent folder
// path used to build SourcePath. Per Feishu's pattern, a failure inside one
// sub-folder is logged and skipped rather than aborting the whole sync (the root
// list error stays fatal so auth/token problems surface loudly).
func (p *Provider) walk(ctx context.Context, accessToken, startURL, pathPrefix string, out *[]provider.Item) error {
	next := startURL
	for next != "" {
		var page childrenPage
		if err := p.doJSON(ctx, accessToken, http.MethodGet, next, &page); err != nil {
			return err
		}
		for _, c := range page.Value {
			if c.Folder != nil {
				child := joinPath(pathPrefix, c.Name)
				*out = append(*out, provider.Item{
					ExternalID: c.ID,
					Title:      c.Name,
					DocType:    "folder",
					SourcePath: child,
					OwnerID:    ownerID(c),
					IsFolder:   true,
				})
				childURL := fmt.Sprintf("%s/me/drive/items/%s/children", graphBaseURL, c.ID)
				if err := p.walk(ctx, accessToken, childURL, child, out); err != nil {
					slog.Default().Warn("skip onedrive folder", "path", child, "err", err)
				}
				continue
			}
			// Treat anything that isn't a folder as a file (File facet present, or a
			// special item we still want to attempt to archive).
			*out = append(*out, provider.Item{
				ExternalID: c.ID,
				Title:      c.Name,
				DocType:    docTypeFor(c.Name),
				SourcePath: pathPrefix,
				OwnerID:    ownerID(c),
			})
		}
		next = page.NextLink
	}
	return nil
}

func ownerID(c graphChild) string {
	if c.CreatedBy != nil && c.CreatedBy.User != nil {
		return c.CreatedBy.User.ID
	}
	return ""
}

// Export downloads a OneDrive item's content. OneDrive/SharePoint files are stored
// as native OOXML, so no format conversion is needed: GET .../content 302-redirects
// to a short-lived download URL, which the default http.Client follows.
func (p *Provider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
	url := fmt.Sprintf("%s/me/drive/items/%s/content", graphBaseURL, item.ExternalID)
	data, err := p.doBytes(ctx, tok.AccessToken, url)
	if err != nil {
		return nil, fmt.Errorf("microsoft export %q: %w", item.ExternalID, err)
	}

	// item.Title is the OneDrive file name and already includes its extension — do
	// NOT re-append it.
	filename := sanitizeFilename(item.Title)
	ext := extOf(filename)
	ct := contentTypes[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	return &provider.Blob{
		Filename:    filename,
		Format:      ext,
		ContentType: ct,
		Data:        data,
	}, nil
}

// Delete moves the cloud original to the OneDrive recycle bin (recoverable):
// DELETE /me/drive/items/{id} performs a soft delete, the file lands in the user's
// recycle bin where it can be restored.
func (p *Provider) Delete(ctx context.Context, tok *provider.Token, item provider.Item) error {
	url := fmt.Sprintf("%s/me/drive/items/%s", graphBaseURL, item.ExternalID)
	return p.doNoContent(ctx, tok.AccessToken, http.MethodDelete, url)
}

// --- HTTP helpers ---------------------------------------------------------

// doJSON performs a rate-limited Graph request and decodes a JSON response into v.
func (p *Provider) doJSON(ctx context.Context, accessToken, method, url string, v any) error {
	body, err := p.do(ctx, accessToken, method, url)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// doBytes performs a rate-limited GET and returns the raw response body.
func (p *Provider) doBytes(ctx context.Context, accessToken, url string) ([]byte, error) {
	return p.do(ctx, accessToken, http.MethodGet, url)
}

// doNoContent performs a request expecting an empty (2xx) body.
func (p *Provider) doNoContent(ctx context.Context, accessToken, method, url string) error {
	_, err := p.do(ctx, accessToken, method, url)
	return err
}

// do runs one rate-limited, bearer-authenticated Graph request, retrying with
// backoff on HTTP 429 (honoring Retry-After) — mirroring feishu's call() intent.
func (p *Provider) do(ctx context.Context, accessToken, method, url string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if err := p.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")

		resp, err := p.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", method, url, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read body: %w", readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAPIRetries {
			backoff := retryAfter(resp.Header.Get("Retry-After"), attempt)
			slog.Default().Warn("microsoft graph rate limited; backing off",
				"url", url, "attempt", attempt+1, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("%s %s: status=%d body=%s", method, url, resp.StatusCode, truncate(string(body), 256))
		}
		return body, nil
	}
}

// retryAfter parses a Retry-After header (delta-seconds) into a duration, falling
// back to exponential backoff capped at 30s when the header is absent/unparseable.
func retryAfter(header string, attempt int) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d > 60*time.Second {
				d = 60 * time.Second
			}
			return d
		}
	}
	backoff := time.Duration(1<<uint(attempt)) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	return backoff
}

// --- pure helpers ---------------------------------------------------------

func tokenFrom(t *oauth2.Token) *provider.Token {
	pt := &provider.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
	}
	if !t.Expiry.IsZero() {
		pt.AccessExpiresAt = t.Expiry
	}
	return pt
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func sanitizeFilename(name string) string {
	if name == "" {
		name = "untitled"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
