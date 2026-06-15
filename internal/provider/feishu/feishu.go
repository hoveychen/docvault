// Package feishu implements provider.Provider for Feishu/Lark using the official
// oapi-sdk-go v3. Auth is per-user (user_access_token) — see docs/architecture.md
// for why a tenant-wide token cannot reach every member's documents.
package feishu

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkauthen "github.com/larksuite/oapi-sdk-go/v3/service/authen/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"

	"github.com/hoveychen/docvault/internal/config"
	"github.com/hoveychen/docvault/internal/provider"
)

const providerKey = "feishu"

// exportable maps a Feishu doc type to the export file extension we request.
// Types not present here are skipped during sync (recorded, not exported).
var exportable = map[string]string{
	"docx":  "docx",
	"doc":   "docx",
	"sheet": "xlsx",
}

var contentTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pdf":  "application/pdf",
}

// Provider is the Feishu implementation.
type Provider struct {
	client  *lark.Client
	baseURL string // open.feishu.cn or open.larksuite.com base
}

// New builds the provider from Feishu app credentials.
func New(cfg config.FeishuConfig) *Provider {
	baseURL := lark.FeishuBaseUrl
	if strings.EqualFold(cfg.Domain, "lark") {
		baseURL = lark.LarkBaseUrl
	}
	client := lark.NewClient(cfg.AppID, cfg.AppSecret, lark.WithOpenBaseUrl(baseURL))
	return &Provider{client: client, baseURL: baseURL}
}

func (p *Provider) Key() string { return providerKey }

// AuthCodeURL builds the authorization redirect (Feishu authen v1, scope-aware).
func (p *Provider) AuthCodeURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	// Read-only scopes sufficient to list + export drive documents.
	q.Set("scope", "drive:drive:readonly docs:document:readonly")
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

// List walks the user's drive recursively starting from the root folder.
func (p *Provider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	var items []provider.Item
	if err := p.walk(ctx, tok.AccessToken, "", "", &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Provider) walk(ctx context.Context, accessToken, folderToken, pathPrefix string, out *[]provider.Item) error {
	pageToken := ""
	for {
		b := larkdrive.NewListFileReqBuilder().PageSize(200)
		if folderToken != "" {
			b.FolderToken(folderToken)
		}
		if pageToken != "" {
			b.PageToken(pageToken)
		}
		resp, err := p.client.Drive.File.List(ctx, b.Build(), larkcore.WithUserAccessToken(accessToken))
		if err != nil {
			return fmt.Errorf("list folder %q: %w", folderToken, err)
		}
		if !resp.Success() {
			return fmt.Errorf("list folder %q: code=%d msg=%s", folderToken, resp.Code, resp.Msg)
		}

		for _, f := range resp.Data.Files {
			name := strDeref(f.Name)
			typ := strDeref(f.Type)
			token := strDeref(f.Token)
			if typ == "folder" {
				child := joinPath(pathPrefix, name)
				if err := p.walk(ctx, accessToken, token, child, out); err != nil {
					return err
				}
				continue
			}
			*out = append(*out, provider.Item{
				ExternalID: token,
				Title:      name,
				DocType:    typ,
				SourcePath: pathPrefix,
			})
		}

		if resp.Data.HasMore != nil && *resp.Data.HasMore && resp.Data.NextPageToken != nil {
			pageToken = *resp.Data.NextPageToken
			continue
		}
		return nil
	}
}

// Export creates an export task, polls it to completion, and downloads the bytes.
func (p *Provider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
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
	createResp, err := p.client.Drive.ExportTask.Create(ctx, createReq, opt)
	if err != nil {
		return nil, fmt.Errorf("create export task: %w", err)
	}
	if !createResp.Success() || createResp.Data.Ticket == nil {
		return nil, fmt.Errorf("create export task: code=%d msg=%s", createResp.Code, createResp.Msg)
	}
	ticket := *createResp.Data.Ticket

	fileToken, err := p.pollExport(ctx, at, ticket, item.ExternalID, opt)
	if err != nil {
		return nil, err
	}

	dlReq := larkdrive.NewDownloadExportTaskReqBuilder().FileToken(fileToken).Build()
	dlResp, err := p.client.Drive.ExportTask.Download(ctx, dlReq, opt)
	if err != nil {
		return nil, fmt.Errorf("download export: %w", err)
	}
	if !dlResp.Success() {
		return nil, fmt.Errorf("download export: code=%d msg=%s", dlResp.Code, dlResp.Msg)
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

// pollExport polls the export task until it succeeds, returning the result file token.
func (p *Provider) pollExport(ctx context.Context, accessToken, ticket, token string, opt larkcore.RequestOptionFunc) (string, error) {
	const maxAttempts = 60 // ~60s with 1s interval
	for attempt := 0; attempt < maxAttempts; attempt++ {
		getReq := larkdrive.NewGetExportTaskReqBuilder().Ticket(ticket).Token(token).Build()
		getResp, err := p.client.Drive.ExportTask.Get(ctx, getReq, opt)
		if err != nil {
			return "", fmt.Errorf("get export task: %w", err)
		}
		if !getResp.Success() || getResp.Data.Result == nil {
			return "", fmt.Errorf("get export task: code=%d msg=%s", getResp.Code, getResp.Msg)
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
