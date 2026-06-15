// Package google implements provider.Provider for Google Workspace / Google Drive
// using the official google.golang.org/api/drive/v3 SDK with golang.org/x/oauth2.
// Auth is per-user (an OAuth user token with the full drive scope) so listing,
// exporting, and deleting all act as the authorizing user — mirroring the Feishu
// provider's user_access_token model.
package google

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/hoveychen/docvault/internal/provider"
)

const (
	apiRatePerSec = 10 // conservative steady-state request rate
	apiBurst      = 10

	folderMimeType = "application/vnd.google-apps.folder"

	// driveScope is the FULL drive scope. It is required (rather than a readonly
	// scope) because Delete must move/modify files; matching feishu, which also
	// supports delete.
	driveScope = "https://www.googleapis.com/auth/drive"
)

// nativeExports maps a Google-native (Workspace) mime type to the office format we
// export it as. Files.Export only works for these native types; everything else is
// a binary upload downloaded raw via alt=media.
//
// NOTE: Files.Export has a ~10MB limit per Google's documentation; native docs
// larger than that return an error, which the engine treats as a non-fatal
// per-item failure (log-and-skip). We cannot raise this limit from the client.
var nativeExports = map[string]struct {
	ext         string
	contentType string
}{
	"application/vnd.google-apps.document": {
		ext:         "docx",
		contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	},
	"application/vnd.google-apps.spreadsheet": {
		ext:         "xlsx",
		contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	},
	"application/vnd.google-apps.presentation": {
		ext:         "pptx",
		contentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	},
	"application/vnd.google-apps.drawing": {
		ext:         "pdf",
		contentType: "application/pdf",
	},
}

// extContentTypes provides a best-effort content type for common downloaded
// binary extensions; falls back to application/octet-stream.
var extContentTypes = map[string]string{
	"pdf":  "application/pdf",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"txt":  "text/plain",
	"csv":  "text/csv",
	"zip":  "application/zip",
}

// Provider is one Google Workspace connection (one OAuth client / app).
type Provider struct {
	key     string
	label   string
	cfg     *oauth2.Config
	limiter *rate.Limiter
}

// register the google factory so provider.Build(ConnDef{Type:"google", …}) works
// once this package is imported for its side effects (see internal/app).
func init() {
	provider.RegisterFactory("google", func(def provider.ConnDef) (provider.Provider, error) {
		return New(def), nil
	})
}

// New builds a provider for one Google OAuth connection. AppID/AppSecret are the
// OAuth client id/secret; Domain is unused for Google.
func New(def provider.ConnDef) *Provider {
	cfg := &oauth2.Config{
		ClientID:     def.AppID,
		ClientSecret: def.AppSecret,
		Endpoint:     googleoauth.Endpoint,
		Scopes:       []string{driveScope},
	}
	return &Provider{
		key:     def.Key,
		label:   def.Label,
		cfg:     cfg,
		limiter: rate.NewLimiter(apiRatePerSec, apiBurst),
	}
}

func (p *Provider) Key() string   { return p.key }
func (p *Provider) Label() string { return p.label }

// wait blocks on the rate limiter before an API call.
func (p *Provider) wait(ctx context.Context) error {
	return p.limiter.Wait(ctx)
}

// AuthCodeURL builds the OAuth authorization URL. AccessTypeOffline +
// ApprovalForce guarantee a refresh token is returned even on re-consent (Google
// otherwise omits the refresh token on subsequent authorizations).
func (p *Provider) AuthCodeURL(state, redirectURI string) string {
	cfg := p.withRedirect(redirectURI)
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// withRedirect returns a shallow copy of the base config with RedirectURL set, so
// concurrent flows with different callback URLs don't race on a shared field.
func (p *Provider) withRedirect(redirectURI string) *oauth2.Config {
	c := *p.cfg
	c.RedirectURL = redirectURI
	return &c
}

// Exchange swaps an authorization code for tokens and reads the authorizing user's
// identity from Drive's About.Get.
func (p *Provider) Exchange(ctx context.Context, code, redirectURI string) (*provider.Token, *provider.Identity, error) {
	cfg := p.withRedirect(redirectURI)
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("google exchange: %w", err)
	}
	token := tokenFromOAuth(tok)

	svc, err := p.driveService(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("google exchange (drive svc): %w", err)
	}
	if err := p.wait(ctx); err != nil {
		return nil, nil, err
	}
	about, err := svc.About.Get().Fields("user").Context(ctx).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("google about: %w", err)
	}
	id := &provider.Identity{}
	if about.User != nil {
		id.ExternalUserID = about.User.PermissionId
		id.DisplayName = about.User.DisplayName
		id.Email = about.User.EmailAddress
		id.AvatarURL = about.User.PhotoLink
	}
	return token, id, nil
}

// Refresh obtains a fresh access token from a refresh token. Google's TokenSource
// transparently refreshes; we read the resulting token. Google typically does not
// return a new refresh token on refresh, so we keep the original one if the
// response omits it.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*provider.Token, error) {
	src := p.cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("google refresh: %w", err)
	}
	out := tokenFromOAuth(tok)
	if out.RefreshToken == "" {
		out.RefreshToken = refreshToken
	}
	return out, nil
}

// driveService builds a Drive v3 service authed with the user's access token.
func (p *Provider) driveService(ctx context.Context, tok *provider.Token) (*drive.Service, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: tok.AccessToken,
		Expiry:      tok.AccessExpiresAt,
	})
	return drive.NewService(ctx, option.WithTokenSource(ts))
}

// List walks the user's Drive recursively starting from the root folder.
func (p *Provider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	svc, err := p.driveService(ctx, tok)
	if err != nil {
		return nil, fmt.Errorf("google list (drive svc): %w", err)
	}
	var items []provider.Item
	if err := p.walk(ctx, svc, "root", "", &items); err != nil {
		return nil, err
	}
	return items, nil
}

// walk lists the direct children of folderID and recurses into sub-folders. A
// failure inside one sub-folder is logged and skipped so one bad folder doesn't
// abort the whole sync (the root list error stays fatal so auth/token problems
// surface loudly), mirroring the Feishu provider.
func (p *Provider) walk(ctx context.Context, svc *drive.Service, folderID, pathPrefix string, out *[]provider.Item) error {
	pageToken := ""
	for {
		if err := p.wait(ctx); err != nil {
			return err
		}
		call := svc.Files.List().
			Q(fmt.Sprintf("'%s' in parents and trashed=false", folderID)).
			Fields("nextPageToken, files(id,name,mimeType,owners)").
			PageSize(1000).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return fmt.Errorf("list folder %q: %w", folderID, err)
		}
		for _, f := range resp.Files {
			ownerID := ""
			if len(f.Owners) > 0 {
				ownerID = f.Owners[0].PermissionId
			}
			if f.MimeType == folderMimeType {
				child := joinPath(pathPrefix, f.Name)
				// Record the folder itself (so its cloud original can be deleted once
				// everything under it is archived), then recurse into it.
				*out = append(*out, provider.Item{
					ExternalID: f.Id,
					Title:      f.Name,
					DocType:    "folder",
					SourcePath: child,
					OwnerID:    ownerID,
					IsFolder:   true,
				})
				if err := p.walk(ctx, svc, f.Id, child, out); err != nil {
					slog.Default().Warn("skip drive folder", "path", child, "err", err)
				}
				continue
			}
			*out = append(*out, provider.Item{
				ExternalID: f.Id,
				Title:      f.Name,
				// Store the google mime type in DocType; Export maps it. The engine only
				// uses DocType as an opaque label, so this is safe.
				DocType:    f.MimeType,
				SourcePath: pathPrefix,
				OwnerID:    ownerID,
			})
		}
		if resp.NextPageToken == "" {
			return nil
		}
		pageToken = resp.NextPageToken
	}
}

// Export turns one item into portable bytes. Google-native docs go through
// Files.Export to an office format; binary uploads are downloaded raw via alt=media
// to keep their original bytes.
func (p *Provider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
	svc, err := p.driveService(ctx, tok)
	if err != nil {
		return nil, fmt.Errorf("google export (drive svc): %w", err)
	}

	if exp, ok := nativeExports[item.DocType]; ok {
		if err := p.wait(ctx); err != nil {
			return nil, err
		}
		// Files.Export has a ~10MB cap per Google; larger native docs error here and
		// are treated by the engine as a non-fatal per-item failure (log-and-skip).
		resp, err := svc.Files.Export(item.ExternalID, exp.contentType).Context(ctx).Download()
		if err != nil {
			return nil, fmt.Errorf("export %q: %w", item.ExternalID, err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read export bytes: %w", err)
		}
		return &provider.Blob{
			Filename:    sanitizeFilename(item.Title) + "." + exp.ext,
			Format:      exp.ext,
			ContentType: exp.contentType,
			Data:        data,
		}, nil
	}

	// Binary upload: download original bytes (alt=media).
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	resp, err := svc.Files.Get(item.ExternalID).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("download %q: %w", item.ExternalID, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read file bytes: %w", err)
	}

	filename := sanitizeFilename(item.Title)
	ext := extOf(filename)
	ct := extContentTypes[strings.ToLower(ext)]
	if ct == "" {
		// Prefer the HTTP-reported content type when the filename has no useful
		// extension; fall back to octet-stream.
		ct = resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/octet-stream"
		}
	}
	return &provider.Blob{
		Filename:    filename,
		Format:      ext,
		ContentType: ct,
		Data:        data,
	}, nil
}

// Delete moves the cloud original to Drive's trash (recoverable), mirroring
// feishu's "move to trash" semantics. We use Files.Update with Trashed=true rather
// than Files.Delete because Files.Delete is a PERMANENT delete in Drive (it skips
// the trash) — trashing is the safer, reversible default.
func (p *Provider) Delete(ctx context.Context, tok *provider.Token, item provider.Item) error {
	svc, err := p.driveService(ctx, tok)
	if err != nil {
		return fmt.Errorf("google delete (drive svc): %w", err)
	}
	if err := p.wait(ctx); err != nil {
		return err
	}
	_, err = svc.Files.Update(item.ExternalID, &drive.File{Trashed: true}).
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("trash %q: %w", item.ExternalID, err)
	}
	return nil
}

func tokenFromOAuth(tok *oauth2.Token) *provider.Token {
	return &provider.Token{
		AccessToken:     tok.AccessToken,
		RefreshToken:    tok.RefreshToken,
		AccessExpiresAt: tok.Expiry,
	}
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// extOf returns the lowercase extension (without dot) of a filename, or "".
func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return ""
}

func sanitizeFilename(name string) string {
	if name == "" {
		name = "untitled"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}
