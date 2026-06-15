// Package tencent implements provider.Provider for Tencent Docs (腾讯文档,
// docs.qq.com/open) using the documented OAuth2 + OpenAPI REST endpoints. No
// official Go SDK exists, so every call is raw net/http.
//
// Endpoints and field names below are taken from the official open-platform docs
// (https://docs.qq.com/open/document/app/...). Where a runtime detail can only be
// confirmed with live credentials (e.g. the exact OOXML container an export
// produces), that is called out with an `// UNVERIFIED:` note rather than left as
// an ungrounded guess.
package tencent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/hoveychen/docvault/internal/provider"
)

const (
	// OAuth endpoints (docs.qq.com/open/document/app/oauth2/*). Both authorize and
	// token are GET requests.
	oauthAuthorizeURL = "https://docs.qq.com/oauth/v2/authorize"
	oauthTokenURL     = "https://docs.qq.com/oauth/v2/token"
	oauthUserInfoURL  = "https://docs.qq.com/oauth/v2/userinfo"

	// OpenAPI REST base (docs.qq.com/open/document/app/openapi/v2/*).
	apiBase = "https://docs.qq.com/openapi"

	apiRatePerSec = 5 // conservative steady-state request rate
	apiBurst      = 5
	maxAPIRetries = 6

	// Tencent throttles with the standard HTTP 429; we back off and retry on it.
	codeRateLimited = -429

	// Page size for the folder-listing endpoint (`limit` query param).
	listLimit = 100
)

// exportSpec maps a Tencent Docs doc type to the export-task `exportType` we
// request and the file extension we store. Per the async-export docs the source
// types are doc / sheet / slide / pdf / smartcanvas. Per-item export failures are
// non-fatal (the engine logs and skips them).
//
// UNVERIFIED: the exact container produced by exportType "doc"/"sheet"/"slide"
// (i.e. whether "doc" yields .docx and "sheet" yields .xlsx) needs a live export
// to confirm; we assume the modern OOXML containers, which is the useful archive
// format. "pdf" is unambiguous.
var exportSpec = map[string]struct{ exportType, ext string }{
	"doc":         {"doc", "docx"},
	"sheet":       {"sheet", "xlsx"},
	"slide":       {"slide", "pptx"},
	"pdf":         {"pdf", "pdf"},
	"smartcanvas": {"doc", "docx"},
}

var contentTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"pdf":  "application/pdf",
}

// Provider is one Tencent Docs connection (one open-platform app).
type Provider struct {
	key       string
	label     string
	appID     string // OAuth client_id (Tencent "AppID")
	appSecret string // OAuth client_secret (Tencent "AppSecret")
	http      *http.Client
	limiter   *rate.Limiter

	// openIDs caches access-token → the authorizing user's openID, which every
	// authenticated OpenAPI call must send in the Open-Id header. It is resolved
	// lazily via the userinfo endpoint (which needs only the access token), so the
	// stateless Provider interface (List/Export/Delete receive just a *Token) need
	// not change.
	openIDs sync.Map // map[string]string
}

// register the tencent factory so provider.Build(ConnDef{Type:"tencent", …})
// works once this package is imported for its side effects (see internal/app).
func init() {
	provider.RegisterFactory("tencent", func(def provider.ConnDef) (provider.Provider, error) {
		return New(def), nil
	})
}

// New builds a provider for one Tencent Docs connection. Domain is unused (Tencent
// has a single open-platform host) but accepted for parity with other providers.
func New(def provider.ConnDef) *Provider {
	label := def.Label
	if label == "" {
		label = "腾讯文档"
	}
	return &Provider{
		key:       def.Key,
		label:     label,
		appID:     def.AppID,
		appSecret: def.AppSecret,
		http:      &http.Client{Timeout: 60 * time.Second},
		limiter:   rate.NewLimiter(apiRatePerSec, apiBurst),
	}
}

func (p *Provider) Key() string   { return p.key }
func (p *Provider) Label() string { return p.label }

// AuthCodeURL builds the OAuth2 authorization redirect:
//
//	GET https://docs.qq.com/oauth/v2/authorize?client_id=…&redirect_uri=…
//	    &response_type=code&scope=all&state=…
//
// scope is the documented fixed value "all".
func (p *Provider) AuthCodeURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", p.appID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "all")
	q.Set("state", state)
	return oauthAuthorizeURL + "?" + q.Encode()
}

// tokenResponse is the token-endpoint payload. Per the docs it carries the OAuth2
// standard fields plus user_id (the user's "Open ID") and a comma-separated scope.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	Scope        string `json:"scope"`

	// Error envelope: the token endpoint may use OAuth2-standard error fields or
	// Tencent's ret/msg. Parse both.
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
	Ret       int    `json:"ret"`
	Msg       string `json:"msg"`
}

func (t *tokenResponse) failed() (bool, string) {
	if t.Error != "" {
		return true, fmt.Sprintf("%s: %s", t.Error, t.ErrorDesc)
	}
	if t.Ret != 0 {
		return true, fmt.Sprintf("ret=%d msg=%s", t.Ret, t.Msg)
	}
	if t.AccessToken == "" {
		return true, "no access_token in token response"
	}
	return false, ""
}

func (t *tokenResponse) token() *provider.Token {
	// The refresh token is documented to last one year; the token response does
	// not carry an explicit refresh TTL, so RefreshExpiresAt is left zero (the
	// engine refreshes on access-token expiry, which is what Expired() checks).
	return &provider.Token{
		AccessToken:     t.AccessToken,
		RefreshToken:    t.RefreshToken,
		AccessExpiresAt: expiry(t.ExpiresIn),
	}
}

// getToken performs an OAuth2 token-endpoint GET (Tencent uses GET, not POST) and
// decodes the JSON response. Used by both Exchange and Refresh.
func (p *Provider) getToken(ctx context.Context, q url.Values) (*tokenResponse, error) {
	var tr *tokenResponse
	err := p.call(ctx, "token", func() (bool, int, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthTokenURL+"?"+q.Encode(), nil)
		if err != nil {
			return false, 0, "", err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := p.http.Do(req)
		if err != nil {
			return false, 0, "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, 0, "", err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return false, codeRateLimited, "rate limited", nil
		}
		var parsed tokenResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return false, resp.StatusCode, string(body), fmt.Errorf("decode token response: %w", err)
		}
		if bad, msg := parsed.failed(); bad {
			return false, resp.StatusCode, msg, nil
		}
		tr = &parsed
		return true, 0, "", nil
	})
	if err != nil {
		return nil, err
	}
	return tr, nil
}

// Exchange swaps an authorization code for tokens + the authorizing user's
// identity. Identity (openID/nick/avatar) comes from the userinfo endpoint.
func (p *Provider) Exchange(ctx context.Context, code, redirectURI string) (*provider.Token, *provider.Identity, error) {
	q := url.Values{}
	q.Set("client_id", p.appID)
	q.Set("client_secret", p.appSecret)
	q.Set("grant_type", "authorization_code")
	q.Set("code", code)
	q.Set("redirect_uri", redirectURI)
	tr, err := p.getToken(ctx, q)
	if err != nil {
		return nil, nil, fmt.Errorf("tencent exchange: %w", err)
	}
	tok := tr.token()

	identity := &provider.Identity{ExternalUserID: tr.UserID}
	if info, ierr := p.fetchUserInfo(ctx, tok.AccessToken); ierr == nil {
		if info.openID() != "" {
			identity.ExternalUserID = info.openID()
		}
		identity.DisplayName = info.Data.Nick
		identity.AvatarURL = info.Data.Avatar
		// Seed the Open-Id cache so the first List/Export need not re-fetch.
		if info.openID() != "" {
			p.openIDs.Store(tok.AccessToken, info.openID())
		}
	} else {
		slog.Default().Warn("tencent userinfo failed at exchange", "err", ierr)
	}
	return tok, identity, nil
}

// Refresh obtains a fresh access token from a refresh token.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*provider.Token, error) {
	q := url.Values{}
	q.Set("client_id", p.appID)
	q.Set("client_secret", p.appSecret)
	q.Set("grant_type", "refresh_token")
	q.Set("refresh_token", refreshToken)
	tr, err := p.getToken(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("tencent refresh: %w", err)
	}
	return tr.token(), nil
}

// userInfoResponse is the /oauth/v2/userinfo payload.
type userInfoResponse struct {
	Ret  int    `json:"ret"`
	Msg  string `json:"msg"`
	Data struct {
		OpenID  string `json:"openID"`
		Nick    string `json:"nick"`
		Avatar  string `json:"avatar"`
		UnionID string `json:"unionID"`
	} `json:"data"`
}

func (u *userInfoResponse) openID() string { return u.Data.OpenID }

// fetchUserInfo resolves the authorizing user via the access token alone. It does
// NOT go through callAPI (which would need the Open-Id we're trying to obtain).
func (p *Provider) fetchUserInfo(ctx context.Context, accessToken string) (*userInfoResponse, error) {
	q := url.Values{}
	q.Set("access_token", accessToken)
	var info *userInfoResponse
	err := p.call(ctx, "userinfo", func() (bool, int, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthUserInfoURL+"?"+q.Encode(), nil)
		if err != nil {
			return false, 0, "", err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := p.http.Do(req)
		if err != nil {
			return false, 0, "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, 0, "", err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return false, codeRateLimited, "rate limited", nil
		}
		var parsed userInfoResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return false, resp.StatusCode, string(body), fmt.Errorf("decode userinfo: %w", err)
		}
		if parsed.Ret != 0 || parsed.openID() == "" {
			return false, resp.StatusCode, fmt.Sprintf("ret=%d msg=%s", parsed.Ret, parsed.Msg), nil
		}
		info = &parsed
		return true, 0, "", nil
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

// resolveOpenID returns the openID for an access token, fetching+caching it via
// userinfo on first use.
func (p *Provider) resolveOpenID(ctx context.Context, accessToken string) (string, error) {
	if v, ok := p.openIDs.Load(accessToken); ok {
		return v.(string), nil
	}
	info, err := p.fetchUserInfo(ctx, accessToken)
	if err != nil {
		return "", err
	}
	p.openIDs.Store(accessToken, info.openID())
	return info.openID(), nil
}

// fileEntry is one element of the folder-listing response. Per the docs each item
// carries ID, title, type ("folder"|"doc"), url and timestamps; ownerID/isOwner
// appear in metadata and (best-effort) in list items for delete gating.
type fileEntry struct {
	ID      string `json:"ID"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	OwnerID string `json:"ownerID"`
}

func (f fileEntry) isFolder() bool { return f.Type == "folder" }

// listResponse wraps the paginated folder listing:
//
//	{"ret":0,"msg":"...","data":{"next":20,"list":[ … ]}}
//
// `next` is the offset to pass as `start` on the subsequent request.
type listResponse struct {
	Ret  int    `json:"ret"`
	Msg  string `json:"msg"`
	Data struct {
		Next int         `json:"next"`
		List []fileEntry `json:"list"`
	} `json:"data"`
}

// List walks the user's drive recursively from the root ("我的文档"), descending
// into every folder. A failure inside one sub-folder is logged and skipped (the
// root listing error stays fatal so auth/token problems surface loudly), mirroring
// the feishu provider.
func (p *Provider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	var items []provider.Item
	if err := p.walk(ctx, tok.AccessToken, "", "", &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Provider) walk(ctx context.Context, accessToken, folderID, pathPrefix string, out *[]provider.Item) error {
	start := 0
	for {
		// GET {apiBase}/drive/v2/folders/{folderID}?start=&limit=  (empty folderID
		// → the user's "我的文档" root).
		endpoint := fmt.Sprintf("%s/drive/v2/folders/%s?start=%d&limit=%d",
			apiBase, url.PathEscape(folderID), start, listLimit)
		var resp listResponse
		if err := p.callAPI(ctx, http.MethodGet, endpoint, accessToken, nil, "", &resp); err != nil {
			return err
		}
		if resp.Ret != 0 {
			return fmt.Errorf("list folder %q: ret=%d msg=%s", folderID, resp.Ret, resp.Msg)
		}

		for _, f := range resp.Data.List {
			if f.ID == "" {
				continue
			}
			if f.isFolder() {
				child := joinPath(pathPrefix, f.Title)
				*out = append(*out, provider.Item{
					ExternalID: f.ID,
					Title:      f.Title,
					DocType:    "folder",
					SourcePath: child,
					OwnerID:    f.OwnerID,
					IsFolder:   true,
				})
				if err := p.walk(ctx, accessToken, f.ID, child, out); err != nil {
					slog.Default().Warn("skip tencent folder", "path", child, "err", err)
				}
				continue
			}
			*out = append(*out, provider.Item{
				ExternalID: f.ID,
				Title:      f.Title,
				DocType:    f.Type,
				SourcePath: pathPrefix,
				OwnerID:    f.OwnerID,
			})
		}

		// Stop when a short page is returned or the cursor does not advance.
		if len(resp.Data.List) < listLimit || resp.Data.Next <= start {
			return nil
		}
		start = resp.Data.Next
	}
}

// createExportResp is the async-export-task response: data.operationID.
type createExportResp struct {
	Ret  int    `json:"ret"`
	Msg  string `json:"msg"`
	Data struct {
		OperationID string `json:"operationID"`
	} `json:"data"`
}

// exportProgressResp is the export-progress response: data.progress (0..100) and
// data.url (a 24h signed download link, present on success).
type exportProgressResp struct {
	Ret  int    `json:"ret"`
	Msg  string `json:"msg"`
	Data struct {
		Progress int    `json:"progress"`
		URL      string `json:"url"`
	} `json:"data"`
}

// Export performs the documented three-step async export: create task → poll
// progress → download the signed URL.
func (p *Provider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
	spec, ok := exportSpec[item.DocType]
	if !ok {
		return nil, fmt.Errorf("doc type %q is not exportable", item.DocType)
	}
	at := tok.AccessToken

	// Step 1: create the export task (form-encoded body, exportType).
	form := url.Values{}
	form.Set("exportType", spec.exportType)
	createEndpoint := fmt.Sprintf("%s/drive/v2/files/%s/async-export", apiBase, url.PathEscape(item.ExternalID))
	var cre createExportResp
	if err := p.callAPI(ctx, http.MethodPost, createEndpoint, at,
		[]byte(form.Encode()), "application/x-www-form-urlencoded", &cre); err != nil {
		return nil, err
	}
	if cre.Ret != 0 {
		return nil, fmt.Errorf("create export task: ret=%d msg=%s", cre.Ret, cre.Msg)
	}
	if cre.Data.OperationID == "" {
		return nil, fmt.Errorf("create export task: no operationID returned")
	}

	// Step 2: poll until a download URL appears.
	dlURL, err := p.pollExport(ctx, at, item.ExternalID, cre.Data.OperationID)
	if err != nil {
		return nil, err
	}

	// Step 3: download the exported bytes from the signed URL (pre-signed, no auth).
	data, err := p.download(ctx, dlURL)
	if err != nil {
		return nil, err
	}

	filename := sanitizeFilename(item.Title) + "." + spec.ext
	return &provider.Blob{
		Filename:    filename,
		Format:      spec.ext,
		ContentType: contentTypes[spec.ext],
		Data:        data,
	}, nil
}

// pollExport polls the export-progress endpoint until a download URL is returned.
func (p *Provider) pollExport(ctx context.Context, accessToken, fileID, opID string) (string, error) {
	const maxAttempts = 120 // ~120s at 1s interval (server gives a 20-min window)
	endpoint := fmt.Sprintf("%s/drive/v2/files/%s/export-progress?operationID=%s",
		apiBase, url.PathEscape(fileID), url.QueryEscape(opID))

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var pr exportProgressResp
		if err := p.callAPI(ctx, http.MethodGet, endpoint, accessToken, nil, "", &pr); err != nil {
			return "", err
		}
		if pr.Ret != 0 {
			return "", fmt.Errorf("export progress: ret=%d msg=%s", pr.Ret, pr.Msg)
		}
		if pr.Data.Progress >= 100 && pr.Data.URL != "" {
			return pr.Data.URL, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return "", fmt.Errorf("export task timed out")
}

// download fetches the signed export URL. The URL is pre-signed (24h validity), so
// no auth headers are sent.
func (p *Provider) download(ctx context.Context, dlURL string) ([]byte, error) {
	var data []byte
	err := p.call(ctx, "download export", func() (bool, int, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
		if err != nil {
			return false, 0, "", err
		}
		resp, err := p.http.Do(req)
		if err != nil {
			return false, 0, "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return false, codeRateLimited, "rate limited", nil
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, resp.StatusCode, "", err
		}
		if resp.StatusCode != http.StatusOK {
			return false, resp.StatusCode, string(b), nil
		}
		data = b
		return true, 0, "", nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Delete moves the cloud original to Tencent's recycle bin (recoverable). The
// delete endpoint defaults to a PERMANENT hard delete, so recoverable=1 is
// mandatory to match docvault's safe "move to trash" semantics.
func (p *Provider) Delete(ctx context.Context, tok *provider.Token, item provider.Item) error {
	endpoint := fmt.Sprintf("%s/drive/v2/files/%s?recoverable=1", apiBase, url.PathEscape(item.ExternalID))
	var resp struct {
		Ret int    `json:"ret"`
		Msg string `json:"msg"`
	}
	if err := p.callAPI(ctx, http.MethodDelete, endpoint, tok.AccessToken, nil, "", &resp); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("delete %q: ret=%d msg=%s", item.ExternalID, resp.Ret, resp.Msg)
	}
	return nil
}

// callAPI performs one rate-limited authenticated OpenAPI call, resolving the
// Open-Id header from the access token and decoding the JSON response into out.
// contentType is set on the request when a body is sent (empty → none).
func (p *Provider) callAPI(ctx context.Context, method, endpoint, accessToken string, body []byte, contentType string, out any) error {
	openID, err := p.resolveOpenID(ctx, accessToken)
	if err != nil {
		return fmt.Errorf("%s %s: resolve openID: %w", method, endpoint, err)
	}
	return p.call(ctx, method+" "+endpoint, func() (bool, int, string, error) {
		var rdr io.Reader
		if body != nil {
			rdr = strings.NewReader(string(body))
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
		if err != nil {
			return false, 0, "", err
		}
		p.setAuthHeaders(req, accessToken, openID)
		if body != nil && contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := p.http.Do(req)
		if err != nil {
			return false, 0, "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, resp.StatusCode, "", err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return false, codeRateLimited, "rate limited", nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return false, resp.StatusCode, string(b), nil
		}
		if out != nil && len(b) > 0 {
			if err := json.Unmarshal(b, out); err != nil {
				return false, resp.StatusCode, string(b), fmt.Errorf("decode response: %w", err)
			}
		}
		return true, 0, "", nil
	})
}

// setAuthHeaders applies the three headers every authenticated OpenAPI call needs:
// Access-Token (bare token), Client-Id (the AppID), and Open-Id (the user openID).
func (p *Provider) setAuthHeaders(req *http.Request, accessToken, openID string) {
	req.Header.Set("Access-Token", accessToken)
	req.Header.Set("Client-Id", p.appID)
	req.Header.Set("Open-Id", openID)
}

// call runs one rate-limited attempt, retrying with exponential backoff when the
// attempt reports the rate-limit sentinel (HTTP 429). Mirrors feishu's call().
func (p *Provider) call(ctx context.Context, label string, attempt func() (ok bool, code int, msg string, err error)) error {
	for i := 0; ; i++ {
		if err := p.limiter.Wait(ctx); err != nil {
			return err
		}
		ok, code, msg, err := attempt()
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if ok {
			return nil
		}
		if code == codeRateLimited && i < maxAPIRetries {
			backoff := time.Duration(1<<uint(i)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			slog.Default().Warn("tencent rate limited; backing off",
				"label", label, "attempt", i+1, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		return fmt.Errorf("%s: code=%d msg=%s", label, code, msg)
	}
}

func expiry(seconds int) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(seconds) * time.Second)
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
