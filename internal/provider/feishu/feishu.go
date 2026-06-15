// Package feishu implements provider.Provider for Feishu/Lark using the official
// oapi-sdk-go v3. Auth is per-user (user_access_token) — see docs/architecture.md
// for why a tenant-wide token cannot reach every member's documents.
package feishu

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkauthen "github.com/larksuite/oapi-sdk-go/v3/service/authen/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
	"golang.org/x/time/rate"

	"github.com/hoveychen/docvault/internal/config"
	"github.com/hoveychen/docvault/internal/provider"
)

// Lark "request trigger frequency limit" error code; we back off and retry on it.
const codeFrequencyLimit = 99991400

const (
	apiRatePerSec = 8 // conservative steady-state request rate per app
	apiBurst      = 8
	maxAPIRetries = 6
)

// exportable maps a Feishu doc type to the export file extension we request via
// the export-task API. Types not present here (e.g. "file") are handled
// separately or skipped. Per-item export failures are non-fatal (the engine
// logs and skips them), so best-effort types like bitable/slides are safe to
// list even where a given tenant rejects the format.
var exportable = map[string]string{
	"docx":    "docx",
	"doc":     "docx",
	"sheet":   "xlsx",
	"bitable": "xlsx",
	"slides":  "pptx",
}

var contentTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"pdf":  "application/pdf",
}

// Provider is one Feishu/Lark org's connection (one self-built app).
type Provider struct {
	key     string
	label   string
	appID   string
	client  *lark.Client
	baseURL string // open.feishu.cn or open.larksuite.com base
	limiter *rate.Limiter
}

// New builds a provider for one org connection.
func New(conn config.FeishuConnection) *Provider {
	baseURL := lark.FeishuBaseUrl
	if strings.EqualFold(conn.Domain, "lark") {
		baseURL = lark.LarkBaseUrl
	}
	client := lark.NewClient(conn.AppID, conn.AppSecret, lark.WithOpenBaseUrl(baseURL))
	return &Provider{
		key: conn.Key, label: conn.Label, appID: conn.AppID, client: client, baseURL: baseURL,
		limiter: rate.NewLimiter(apiRatePerSec, apiBurst),
	}
}

func (p *Provider) Key() string   { return p.key }
func (p *Provider) Label() string { return p.label }

// call runs one rate-limited Feishu API call, retrying with exponential backoff
// when Lark returns the frequency-limit code (99991400). The attempt closure
// performs the SDK call and reports (success, code, msg, transport-error).
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
		if code == codeFrequencyLimit && i < maxAPIRetries {
			backoff := time.Duration(1<<uint(i)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			slog.Default().Warn("feishu rate limited; backing off",
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

// AuthCodeURL builds the authorization redirect (Feishu authen v1, scope-aware).
func (p *Provider) AuthCodeURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("app_id", p.appID) // required by Feishu/Lark to identify the app
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	// Read-only scopes for listing + exporting drive documents and wiki nodes:
	//   - drive:drive:readonly  — list folders/files and download binary files
	//   - wiki:wiki:readonly    — enumerate wiki spaces/nodes
	//   - drive:export:readonly — create export tasks for native docs (docx/sheet/
	//     bitable). WITHOUT it the export task returns 99991679 Unauthorized and
	//     every native doc fails to archive (only raw "file" types download directly).
	// (Deleting cloud originals additionally needs the writable drive:drive scope
	// granted in the app console; docs:document:readonly is NOT a valid Lark scope —
	// error 20043 — so we use drive:export:readonly, the privilege Lark itself names.)
	q.Set("scope", "drive:drive:readonly wiki:wiki:readonly drive:export:readonly")
	return fmt.Sprintf("%s/open-apis/authen/v1/authorize?%s", p.baseURL, q.Encode())
}

// Exchange swaps an authorization code for a user_access_token plus the
// authorizing user's identity (Feishu returns both in one response). The v1 flow
// does not use redirect_uri at exchange time (the SDK supplies the
// app_access_token automatically), so redirectURI is accepted but unused.
func (p *Provider) Exchange(ctx context.Context, code, _ string) (*provider.Token, *provider.Identity, error) {
	req := larkauthen.NewCreateAccessTokenReqBuilder().
		Body(larkauthen.NewCreateAccessTokenReqBodyBuilder().
			GrantType("authorization_code").
			Code(code).
			Build()).
		Build()
	resp, err := p.client.Authen.AccessToken.Create(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("feishu exchange: %w", err)
	}
	if !resp.Success() {
		return nil, nil, fmt.Errorf("feishu exchange: code=%d msg=%s", resp.Code, resp.Msg)
	}
	d := resp.Data
	identity := &provider.Identity{
		ExternalUserID: strDeref(d.OpenId),
		DisplayName:    strDeref(d.Name),
		Email:          firstNonEmpty(strDeref(d.EnterpriseEmail), strDeref(d.Email)),
		AvatarURL:      strDeref(d.AvatarUrl),
	}
	return tokenFromAccessResp(d), identity, nil
}

// Refresh obtains a fresh access token from a refresh token.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*provider.Token, error) {
	req := larkauthen.NewCreateRefreshAccessTokenReqBuilder().
		Body(larkauthen.NewCreateRefreshAccessTokenReqBodyBuilder().
			GrantType("refresh_token").
			RefreshToken(refreshToken).
			Build()).
		Build()
	resp, err := p.client.Authen.RefreshAccessToken.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("feishu refresh: %w", err)
	}
	if !resp.Success() {
		return nil, fmt.Errorf("feishu refresh: code=%d msg=%s", resp.Code, resp.Msg)
	}
	d := resp.Data
	return &provider.Token{
		AccessToken:      strDeref(d.AccessToken),
		RefreshToken:     strDeref(d.RefreshToken),
		AccessExpiresAt:  expiry(d.ExpiresIn),
		RefreshExpiresAt: expiry(d.RefreshExpiresIn),
	}, nil
}

// List walks the user's drive recursively from the root folder, then appends
// every wiki node the user can reach.
func (p *Provider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	var items []provider.Item
	if err := p.walk(ctx, tok.AccessToken, "", "", &items); err != nil {
		return nil, err
	}
	if err := p.listWiki(ctx, tok.AccessToken, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Provider) walk(ctx context.Context, accessToken, folderToken, pathPrefix string, out *[]provider.Item) error {
	pageToken := ""
	for {
		b := larkdrive.NewListFileReqBuilder().PageSize(200).UserIdType("open_id")
		if folderToken != "" {
			b.FolderToken(folderToken)
		}
		if pageToken != "" {
			b.PageToken(pageToken)
		}
		var resp *larkdrive.ListFileResp
		if err := p.call(ctx, fmt.Sprintf("list folder %q", folderToken), func() (bool, int, string, error) {
			var e error
			resp, e = p.client.Drive.File.List(ctx, b.Build(), larkcore.WithUserAccessToken(accessToken))
			if e != nil {
				return false, 0, "", e
			}
			return resp.Success(), resp.Code, resp.Msg, nil
		}); err != nil {
			return err
		}

		for _, f := range resp.Data.Files {
			name := strDeref(f.Name)
			typ := strDeref(f.Type)
			token := strDeref(f.Token)
			if typ == "folder" {
				child := joinPath(pathPrefix, name)
				// Record the folder itself (so its cloud original can be deleted
				// once everything under it is archived), then recurse into it.
				*out = append(*out, provider.Item{
					ExternalID: token,
					Title:      name,
					DocType:    "folder",
					SourcePath: child,
					OwnerID:    strDeref(f.OwnerId),
					IsFolder:   true,
				})
				// A failure inside one sub-folder shouldn't abort the whole sync —
				// log and skip it (the root list error above stays fatal so auth/token
				// problems still surface loudly).
				if err := p.walk(ctx, accessToken, token, child, out); err != nil {
					slog.Default().Warn("skip drive folder", "path", child, "err", err)
				}
				continue
			}
			*out = append(*out, provider.Item{
				ExternalID: token,
				Title:      name,
				DocType:    typ,
				SourcePath: pathPrefix,
				OwnerID:    strDeref(f.OwnerId),
			})
		}

		if resp.Data.HasMore != nil && *resp.Data.HasMore && resp.Data.NextPageToken != nil {
			pageToken = *resp.Data.NextPageToken
			continue
		}
		return nil
	}
}

// listWiki enumerates every wiki space the user can see and recurses each
// space's node tree, appending each node's underlying object as an Item keyed
// by its obj_token / obj_type (so Export handles it like any drive doc).
//
// Wiki is best-effort: a failure listing spaces, or listing nodes within one
// space (Lark sometimes returns rpc errors for spaces the user can see but not
// fully enumerate), is logged and skipped rather than aborting the whole sync —
// otherwise one bad wiki space would lose the entire drive archive.
func (p *Provider) listWiki(ctx context.Context, accessToken string, out *[]provider.Item) error {
	opt := larkcore.WithUserAccessToken(accessToken)
	pageToken := ""
	for {
		b := larkwiki.NewListSpaceReqBuilder().PageSize(50)
		if pageToken != "" {
			b.PageToken(pageToken)
		}
		var resp *larkwiki.ListSpaceResp
		if err := p.call(ctx, "list wiki spaces", func() (bool, int, string, error) {
			var e error
			resp, e = p.client.Wiki.Space.List(ctx, b.Build(), opt)
			if e != nil {
				return false, 0, "", e
			}
			return resp.Success(), resp.Code, resp.Msg, nil
		}); err != nil {
			slog.Default().Warn("skip wiki: list spaces failed", "err", err)
			return nil
		}
		for _, sp := range resp.Data.Items {
			spaceName := strDeref(sp.Name)
			if err := p.walkWikiNodes(ctx, opt, strDeref(sp.SpaceId), "", joinPath("Wiki", spaceName), out); err != nil {
				slog.Default().Warn("skip wiki space", "space", strDeref(sp.SpaceId), "name", spaceName, "err", err)
				continue
			}
		}
		if resp.Data.HasMore != nil && *resp.Data.HasMore && resp.Data.PageToken != nil {
			pageToken = *resp.Data.PageToken
			continue
		}
		return nil
	}
}

func (p *Provider) walkWikiNodes(ctx context.Context, opt larkcore.RequestOptionFunc, spaceID, parentNode, pathPrefix string, out *[]provider.Item) error {
	pageToken := ""
	for {
		b := larkwiki.NewListSpaceNodeReqBuilder().SpaceId(spaceID).PageSize(50)
		if parentNode != "" {
			b.ParentNodeToken(parentNode)
		}
		if pageToken != "" {
			b.PageToken(pageToken)
		}
		var resp *larkwiki.ListSpaceNodeResp
		if err := p.call(ctx, fmt.Sprintf("list wiki nodes (space %s)", spaceID), func() (bool, int, string, error) {
			var e error
			resp, e = p.client.Wiki.SpaceNode.List(ctx, b.Build(), opt)
			if e != nil {
				return false, 0, "", e
			}
			return resp.Success(), resp.Code, resp.Msg, nil
		}); err != nil {
			return err
		}
		for _, n := range resp.Data.Items {
			title := strDeref(n.Title)
			objToken := strDeref(n.ObjToken)
			objType := strDeref(n.ObjType)
			if objToken != "" && objType != "" {
				*out = append(*out, provider.Item{
					ExternalID: objToken,
					Title:      title,
					DocType:    objType,
					SourcePath: pathPrefix,
				})
			}
			if n.HasChild != nil && *n.HasChild {
				child := joinPath(pathPrefix, title)
				if err := p.walkWikiNodes(ctx, opt, spaceID, strDeref(n.NodeToken), child, out); err != nil {
					return err
				}
			}
		}
		if resp.Data.HasMore != nil && *resp.Data.HasMore && resp.Data.PageToken != nil {
			pageToken = *resp.Data.PageToken
			continue
		}
		return nil
	}
}

// downloadBinary fetches a raw drive file (non-document) and keeps its original bytes.
func (p *Provider) downloadBinary(ctx context.Context, accessToken string, item provider.Item) (*provider.Blob, error) {
	opt := larkcore.WithUserAccessToken(accessToken)
	req := larkdrive.NewDownloadFileReqBuilder().FileToken(item.ExternalID).Build()
	var resp *larkdrive.DownloadFileResp
	if err := p.call(ctx, "download file", func() (bool, int, string, error) {
		var e error
		resp, e = p.client.Drive.File.Download(ctx, req, opt)
		if e != nil {
			return false, 0, "", e
		}
		return resp.Success(), resp.Code, resp.Msg, nil
	}); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return nil, fmt.Errorf("read file bytes: %w", err)
	}

	filename := sanitizeFilename(item.Title)
	ext := ""
	if i := strings.LastIndex(filename, "."); i >= 0 && i < len(filename)-1 {
		ext = filename[i+1:]
	}
	return &provider.Blob{
		Filename:    filename,
		Format:      ext,
		ContentType: "application/octet-stream",
		Data:        data,
	}, nil
}

// Export turns one item into portable bytes. Binary files are downloaded raw;
// online documents go through the export-task API.
func (p *Provider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
	if item.DocType == "file" {
		return p.downloadBinary(ctx, tok.AccessToken, item)
	}
	ext, ok := exportable[item.DocType]
	if !ok {
		return nil, fmt.Errorf("doc type %q is not exportable", item.DocType)
	}
	at := tok.AccessToken
	opt := larkcore.WithUserAccessToken(at)

	docType := item.DocType
	createReq := larkdrive.NewCreateExportTaskReqBuilder().
		ExportTask(&larkdrive.ExportTask{
			Type:          &docType,
			Token:         &item.ExternalID,
			FileExtension: &ext,
		}).Build()
	var createResp *larkdrive.CreateExportTaskResp
	if err := p.call(ctx, "create export task", func() (bool, int, string, error) {
		var e error
		createResp, e = p.client.Drive.ExportTask.Create(ctx, createReq, opt)
		if e != nil {
			return false, 0, "", e
		}
		return createResp.Success(), createResp.Code, createResp.Msg, nil
	}); err != nil {
		return nil, err
	}
	if createResp.Data.Ticket == nil {
		return nil, fmt.Errorf("create export task: no ticket returned")
	}
	ticket := *createResp.Data.Ticket

	fileToken, err := p.pollExport(ctx, at, ticket, item.ExternalID, opt)
	if err != nil {
		return nil, err
	}

	dlReq := larkdrive.NewDownloadExportTaskReqBuilder().FileToken(fileToken).Build()
	var dlResp *larkdrive.DownloadExportTaskResp
	if err := p.call(ctx, "download export", func() (bool, int, string, error) {
		var e error
		dlResp, e = p.client.Drive.ExportTask.Download(ctx, dlReq, opt)
		if e != nil {
			return false, 0, "", e
		}
		return dlResp.Success(), dlResp.Code, dlResp.Msg, nil
	}); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(dlResp.File)
	if err != nil {
		return nil, fmt.Errorf("read export bytes: %w", err)
	}

	filename := sanitizeFilename(item.Title) + "." + ext
	return &provider.Blob{
		Filename:    filename,
		Format:      ext,
		ContentType: contentTypes[ext],
		Data:        data,
	}, nil
}

// Delete moves the cloud original to the Feishu/Lark trash (recoverable).
// The drive delete endpoint requires the object type; wiki nodes (whose owner
// we don't capture) are never marked deletable upstream, so they don't reach here.
func (p *Provider) Delete(ctx context.Context, tok *provider.Token, item provider.Item) error {
	req := larkdrive.NewDeleteFileReqBuilder().
		FileToken(item.ExternalID).
		Type(item.DocType).
		Build()
	return p.call(ctx, fmt.Sprintf("delete %s %q", item.DocType, item.ExternalID), func() (bool, int, string, error) {
		resp, e := p.client.Drive.File.Delete(ctx, req, larkcore.WithUserAccessToken(tok.AccessToken))
		if e != nil {
			return false, 0, "", e
		}
		return resp.Success(), resp.Code, resp.Msg, nil
	})
}

// pollExport polls the export task until it succeeds, returning the result file token.
func (p *Provider) pollExport(ctx context.Context, accessToken, ticket, token string, opt larkcore.RequestOptionFunc) (string, error) {
	const maxAttempts = 60 // ~60s with 1s interval
	for attempt := 0; attempt < maxAttempts; attempt++ {
		getReq := larkdrive.NewGetExportTaskReqBuilder().Ticket(ticket).Token(token).Build()
		var getResp *larkdrive.GetExportTaskResp
		if err := p.call(ctx, "get export task", func() (bool, int, string, error) {
			var e error
			getResp, e = p.client.Drive.ExportTask.Get(ctx, getReq, opt)
			if e != nil {
				return false, 0, "", e
			}
			return getResp.Success(), getResp.Code, getResp.Msg, nil
		}); err != nil {
			return "", err
		}
		if getResp.Data.Result == nil {
			return "", fmt.Errorf("get export task: no result")
		}
		res := getResp.Data.Result
		status := 0
		if res.JobStatus != nil {
			status = *res.JobStatus
		}
		switch {
		case status == 0: // success
			if res.FileToken == nil {
				return "", fmt.Errorf("export succeeded but no file token")
			}
			return *res.FileToken, nil
		case status == 1 || status == 2: // initializing / processing
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Second):
			}
		default: // error
			return "", fmt.Errorf("export failed: status=%d msg=%s", status, strDeref(res.JobErrorMsg))
		}
	}
	return "", fmt.Errorf("export task timed out after %ds", maxAttempts)
}

func tokenFromAccessResp(d *larkauthen.CreateAccessTokenRespData) *provider.Token {
	return &provider.Token{
		AccessToken:      strDeref(d.AccessToken),
		RefreshToken:     strDeref(d.RefreshToken),
		AccessExpiresAt:  expiry(d.ExpiresIn),
		RefreshExpiresAt: expiry(d.RefreshExpiresIn),
	}
}

func expiry(seconds *int) time.Time {
	if seconds == nil || *seconds == 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(*seconds) * time.Second)
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
