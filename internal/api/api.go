// Package api exposes docvault's HTTP REST surface.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/hoveychen/docvault/internal/app"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/models"
	"github.com/hoveychen/docvault/internal/provider"
)

const (
	sessionCookie = "docvault_session"
	stateCookie   = "docvault_oauth_state"
)

// Handler holds API dependencies.
type Handler struct {
	app *app.App
}

// NewRouter builds the HTTP handler with all routes mounted.
func NewRouter(a *app.App) http.Handler {
	h := &Handler{app: a}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /api/providers", h.listProviders)
	mux.HandleFunc("GET /api/auth/{provider}/login", h.login)
	mux.HandleFunc("GET /api/auth/{provider}/callback", h.callback)
	mux.HandleFunc("POST /api/auth/logout", h.logout)

	mux.Handle("GET /api/me", h.requireUser(h.me))
	mux.Handle("GET /api/stats", h.requireUser(h.archiveStats))
	mux.Handle("GET /api/documents", h.requireUser(h.listDocuments))
	mux.Handle("GET /api/documents/{id}/download", h.requireUser(h.downloadDocument))
	mux.Handle("GET /api/documents/{id}/attachments/{aid}/download", h.requireUser(h.downloadAttachment))
	mux.Handle("POST /api/documents/delete-source", h.requireUser(h.deleteSource))
	mux.Handle("GET /api/folders", h.requireUser(h.listFolders))
	mux.Handle("POST /api/folders/delete-source", h.requireUser(h.deleteFolderSource))
	mux.Handle("POST /api/sync", h.requireUser(h.startSync))
	mux.Handle("GET /api/sync/status", h.requireUser(h.syncStatus))

	// Admin backend.
	mux.Handle("GET /api/admin/users", h.requireAdmin(h.adminListUsers))
	mux.Handle("POST /api/admin/users/{id}/promote", h.requireAdmin(h.adminPromote))
	mux.Handle("POST /api/admin/users/{id}/demote", h.requireAdmin(h.adminDemote))
	mux.Handle("POST /api/admin/users/{id}/ban", h.requireAdmin(h.adminBan))
	mux.Handle("POST /api/admin/users/{id}/unban", h.requireAdmin(h.adminUnban))
	mux.Handle("GET /api/admin/sync-jobs", h.requireAdmin(h.adminListSyncJobs))
	mux.Handle("POST /api/admin/sync-jobs/{id}/requeue", h.requireAdmin(h.adminRequeueSyncJob))
	mux.Handle("POST /api/admin/resync-all", h.requireAdmin(h.adminResyncAll))
	mux.Handle("GET /api/admin/archive-stats", h.requireAdmin(h.adminArchiveStats))
	mux.Handle("GET /api/admin/sync-failures", h.requireAdmin(h.adminSyncFailures))
	mux.Handle("GET /api/admin/provider-types", h.requireAdmin(h.adminProviderTypes))
	mux.Handle("GET /api/admin/connections", h.requireAdmin(h.adminListConnections))
	mux.Handle("POST /api/admin/connections", h.requireAdmin(h.adminCreateConnection))
	mux.Handle("PUT /api/admin/connections/{id}", h.requireAdmin(h.adminUpdateConnection))
	mux.Handle("DELETE /api/admin/connections/{id}", h.requireAdmin(h.adminDeleteConnection))

	return withCORS(mux)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.app.Pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": h.app.Registry.List()})
}

// login redirects the browser to the provider's OAuth authorization page.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	prov := h.app.Registry.Get(r.PathValue("provider"))
	if prov == nil {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	state := randomToken()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	redirectURI := h.app.Config.PublicURL + "/api/auth/" + prov.Key() + "/callback"
	http.Redirect(w, r, prov.AuthCodeURL(state, redirectURI), http.StatusFound)
}

// callback handles the OAuth redirect: validates state, exchanges the code,
// links the account, and issues a session cookie.
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	prov := h.app.Registry.Get(r.PathValue("provider"))
	if prov == nil {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	stateCk, err := r.Cookie(stateCookie)
	if err != nil || stateCk.Value == "" || stateCk.Value != state {
		writeError(w, http.StatusBadRequest, "invalid oauth state")
		return
	}
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	ctx := r.Context()
	redirectURI := h.app.Config.PublicURL + "/api/auth/" + prov.Key() + "/callback"
	tok, identity, err := prov.Exchange(ctx, code, redirectURI)
	if err != nil {
		h.app.Log.Error("oauth exchange failed", "provider", prov.Key(), "err", err)
		writeError(w, http.StatusBadGateway, "oauth exchange failed")
		return
	}

	accessEnc, refreshEnc, err := h.app.Tokens.Encrypt(tok)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token encryption failed")
		return
	}

	userID, _, err := h.app.Repo.LinkAccount(ctx, db.ProviderAccountUpsert{
		Provider:            prov.Key(),
		ExternalUserID:      identity.ExternalUserID,
		DisplayName:         identity.DisplayName,
		Email:               identity.Email,
		AvatarURL:           identity.AvatarURL,
		AccessTokenEnc:      accessEnc,
		RefreshTokenEnc:     refreshEnc,
		AccessTokenExpires:  tok.AccessExpiresAt,
		RefreshTokenExpires: tok.RefreshExpiresAt,
	})
	if err != nil {
		h.app.Log.Error("link account failed", "err", err)
		writeError(w, http.StatusInternalServerError, "account linking failed")
		return
	}

	sess, err := h.app.Sessions.Issue(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session issue failed")
		return
	}
	h.setSessionCookie(w, sess)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.app.Repo.GetUser(r.Context(), userIDFrom(r))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// archiveStats returns the signed-in user's archived/unarchived breakdown.
func (h *Handler) archiveStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.app.Repo.ArchiveStats(r.Context(), userIDFrom(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := userIDFrom(r)
	docs, err := h.app.Repo.ListDocuments(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if docs == nil {
		docs = []models.Document{}
	}
	// Embedded attachments, fetched once and grouped by document, so a doc with
	// file-attachment blocks shows its sidecars alongside the main export.
	attByDoc := map[string][]models.Attachment{}
	if atts, aerr := h.app.Repo.ListAttachmentsForUser(ctx, userID); aerr == nil {
		for _, a := range atts {
			attByDoc[a.DocumentID] = append(attByDoc[a.DocumentID], a)
		}
	}
	// Compute per-doc deletability: the signed-in user must own the doc, it must
	// be archived, and the original must not already be deleted.
	ownerIDs := map[string]string{} // provider -> this user's external id
	for i := range docs {
		d := &docs[i]
		extID, ok := ownerIDs[d.Provider]
		if !ok {
			if acct, aerr := h.app.Repo.GetAccountForUser(ctx, userID, d.Provider); aerr == nil {
				extID = acct.ExternalUserID
			}
			ownerIDs[d.Provider] = extID
		}
		d.Deletable = d.ObjectKey != "" && d.SourceDeletedAt == nil &&
			d.OwnerExternalID != "" && d.OwnerExternalID == extID
		d.Attachments = attByDoc[d.ID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs})
}

// deleteSource deletes the cloud originals of the given archived documents,
// gating on ownership + archival. Each item is processed independently; the
// response reports per-document outcomes.
func (h *Handler) deleteSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := userIDFrom(r)

	var body struct {
		DocumentIDs []string `json:"document_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.DocumentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "document_ids required")
		return
	}

	docs, err := h.app.Repo.GetDocumentsByIDs(ctx, userID, body.DocumentIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	// Cache provider account + valid token per provider.
	type provCtx struct {
		extID string
		tok   *provider.Token
		prov  provider.Provider
	}
	cache := map[string]*provCtx{}
	results := make([]map[string]string, 0, len(docs))

	for i := range docs {
		d := &docs[i]
		res := map[string]string{"id": d.ID}

		if d.ObjectKey == "" || d.SourceDeletedAt != nil {
			res["status"] = "skipped"
			res["error"] = "not archived or already deleted"
			results = append(results, res)
			continue
		}

		pc := cache[d.Provider]
		if pc == nil {
			pc = &provCtx{prov: h.app.Registry.Get(d.Provider)}
			if acct, aerr := h.app.Repo.GetAccountForUser(ctx, userID, d.Provider); aerr == nil {
				pc.extID = acct.ExternalUserID
				if pc.prov != nil {
					if tok, terr := h.app.Tokens.ValidToken(ctx, acct); terr == nil {
						pc.tok = tok
					}
				}
			}
			cache[d.Provider] = pc
		}

		if pc.prov == nil || pc.tok == nil {
			res["status"] = "error"
			res["error"] = "provider unavailable"
			results = append(results, res)
			continue
		}
		if d.OwnerExternalID == "" || d.OwnerExternalID != pc.extID {
			res["status"] = "forbidden"
			res["error"] = "you are not the owner of this document"
			results = append(results, res)
			continue
		}

		item := provider.Item{ExternalID: d.ExternalID, Title: d.Title, DocType: d.DocType}
		if derr := pc.prov.Delete(ctx, pc.tok, item); derr != nil {
			h.app.Log.Error("delete source failed", "doc_id", d.ID, "err", derr)
			res["status"] = "error"
			res["error"] = "delete failed"
			results = append(results, res)
			continue
		}
		if err := h.app.Repo.MarkSourceDeleted(ctx, userID, d.ID); err != nil {
			res["status"] = "error"
			res["error"] = "deleted in cloud but failed to record"
			results = append(results, res)
			continue
		}
		res["status"] = "deleted"
		results = append(results, res)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) downloadDocument(w http.ResponseWriter, r *http.Request) {
	doc, err := h.app.Repo.GetDocument(r.Context(), userIDFrom(r), r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	filename := doc.Title
	if doc.Format != "" {
		filename += "." + doc.Format
	}

	// Stream the object through the server rather than redirecting to a pre-signed
	// URL: in single-origin deployments (e.g. Muvee) object storage isn't publicly
	// reachable from the browser, so the app must proxy the bytes.
	reader, contentType, size, err := h.app.Store.Open(r.Context(), doc.ObjectKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open object failed")
		return
	}
	defer reader.Close()

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

// downloadAttachment streams one embedded attachment, scoped to the owning user
// via its parent document (so the id can't be used to reach another user's bytes).
func (h *Handler) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	att, err := h.app.Repo.GetAttachment(r.Context(), userIDFrom(r), r.PathValue("aid"))
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	reader, contentType, size, err := h.app.Store.Open(r.Context(), att.ObjectKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open object failed")
		return
	}
	defer reader.Close()

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	filename := att.Filename
	if filename == "" {
		filename = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func (h *Handler) listFolders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := userIDFrom(r)
	folders, err := h.app.Repo.ListFolders(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if folders == nil {
		folders = []models.Folder{}
	}
	ownerIDs := map[string]string{} // provider -> this user's external id
	for i := range folders {
		f := &folders[i]
		extID, ok := ownerIDs[f.Provider]
		if !ok {
			if acct, aerr := h.app.Repo.GetAccountForUser(ctx, userID, f.Provider); aerr == nil {
				extID = acct.ExternalUserID
			}
			ownerIDs[f.Provider] = extID
		}
		f.Deletable, f.NotDeletable = h.app.Repo.FolderDeletability(ctx, userID, f, extID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
}

// deleteFolderSource deletes whole source folders (cascading to trash) when every
// document under them is archived and owned by the user.
func (h *Handler) deleteFolderSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := userIDFrom(r)

	var body struct {
		FolderIDs []string `json:"folder_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.FolderIDs) == 0 {
		writeError(w, http.StatusBadRequest, "folder_ids required")
		return
	}

	folders, err := h.app.Repo.GetFoldersByIDs(ctx, userID, body.FolderIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	type provCtx struct {
		extID string
		tok   *provider.Token
		prov  provider.Provider
	}
	cache := map[string]*provCtx{}
	results := make([]map[string]string, 0, len(folders))

	for i := range folders {
		f := &folders[i]
		res := map[string]string{"id": f.ID}

		pc := cache[f.Provider]
		if pc == nil {
			pc = &provCtx{prov: h.app.Registry.Get(f.Provider)}
			if acct, aerr := h.app.Repo.GetAccountForUser(ctx, userID, f.Provider); aerr == nil {
				pc.extID = acct.ExternalUserID
				if pc.prov != nil {
					if tok, terr := h.app.Tokens.ValidToken(ctx, acct); terr == nil {
						pc.tok = tok
					}
				}
			}
			cache[f.Provider] = pc
		}
		if pc.prov == nil || pc.tok == nil {
			res["status"], res["error"] = "error", "provider unavailable"
			results = append(results, res)
			continue
		}

		if ok, reason := h.app.Repo.FolderDeletability(ctx, userID, f, pc.extID); !ok {
			res["status"], res["error"] = "forbidden", reason
			results = append(results, res)
			continue
		}

		item := provider.Item{ExternalID: f.ExternalID, Title: f.Title, DocType: "folder", IsFolder: true}
		if derr := pc.prov.Delete(ctx, pc.tok, item); derr != nil {
			h.app.Log.Error("delete folder source failed", "folder_id", f.ID, "err", derr)
			res["status"], res["error"] = "error", "delete failed"
			results = append(results, res)
			continue
		}
		if err := h.app.Repo.MarkFolderTreeSourceDeleted(ctx, userID, f); err != nil {
			res["status"], res["error"] = "error", "deleted in cloud but failed to record"
			results = append(results, res)
			continue
		}
		res["status"] = "deleted"
		results = append(results, res)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) startSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := userIDFrom(r)

	active, err := h.app.Repo.HasActiveJob(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue check failed")
		return
	}
	if active {
		writeError(w, http.StatusConflict, "a sync is already in progress")
		return
	}

	// Sync the account this user actually authorized (a docvault user is created
	// per provider+external-user, so they have exactly one linked account).
	accts, err := h.app.Repo.GetAccountsForUser(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account lookup failed")
		return
	}
	if len(accts) == 0 {
		writeError(w, http.StatusBadRequest, "no linked provider account")
		return
	}
	acct := accts[0]

	jobID, err := h.app.Repo.EnqueueSyncJob(ctx, userID, acct.ID, acct.Provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "enqueue failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "queued"})
}

func (h *Handler) syncStatus(w http.ResponseWriter, r *http.Request) {
	job, err := h.app.Repo.LatestJob(r.Context(), userIDFrom(r))
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "none"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "status failed")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, value string) {
	secure := len(h.app.Config.PublicURL) >= 5 && h.app.Config.PublicURL[:5] == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge(),
	})
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
