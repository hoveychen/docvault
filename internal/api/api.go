// Package api exposes docvault's HTTP REST surface.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hoveychen/docvault/internal/app"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/models"
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
	mux.Handle("GET /api/documents", h.requireUser(h.listDocuments))
	mux.Handle("GET /api/documents/{id}/download", h.requireUser(h.downloadDocument))
	mux.Handle("POST /api/sync", h.requireUser(h.startSync))
	mux.Handle("GET /api/sync/status", h.requireUser(h.syncStatus))

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
	writeJSON(w, http.StatusOK, map[string]any{"providers": h.app.Registry.Keys()})
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

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	docs, err := h.app.Repo.ListDocuments(r.Context(), userIDFrom(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if docs == nil {
		docs = []models.Document{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs})
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
	url, err := h.app.Store.PresignDownload(r.Context(), doc.ObjectKey, filename, 5*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "presign failed")
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
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

	// Sync the user's first linked provider (Feishu in the MVP).
	keys := h.app.Registry.Keys()
	if len(keys) == 0 {
		writeError(w, http.StatusServiceUnavailable, "no providers configured")
		return
	}
	acct, err := h.app.Repo.GetAccountForUser(ctx, userID, keys[0])
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusBadRequest, "no linked provider account")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account lookup failed")
		return
	}

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
