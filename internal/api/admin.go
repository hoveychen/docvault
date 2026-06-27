package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/models"
	"github.com/hoveychen/docvault/internal/provider"
)

func (h *Handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.app.Repo.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if users == nil {
		users = []models.User{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (h *Handler) adminPromote(w http.ResponseWriter, r *http.Request) {
	h.setRole(w, r, models.RoleAdmin)
}

func (h *Handler) adminDemote(w http.ResponseWriter, r *http.Request) {
	// Don't allow removing the last remaining admin.
	if h.wouldRemoveLastAdmin(w, r) {
		return
	}
	h.setRole(w, r, models.RoleMember)
}

func (h *Handler) setRole(w http.ResponseWriter, r *http.Request, role string) {
	id := r.PathValue("id")
	if err := h.app.Repo.SetUserRole(r.Context(), id, role); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) adminBan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == userIDFrom(r) {
		writeError(w, http.StatusBadRequest, "you cannot ban yourself")
		return
	}
	if h.wouldRemoveLastAdmin(w, r) {
		return
	}
	h.setBanned(w, r, id, true)
}

func (h *Handler) adminUnban(w http.ResponseWriter, r *http.Request) {
	h.setBanned(w, r, r.PathValue("id"), false)
}

func (h *Handler) setBanned(w http.ResponseWriter, r *http.Request, id string, banned bool) {
	if err := h.app.Repo.SetUserBanned(r.Context(), id, banned); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// wouldRemoveLastAdmin returns true (and writes an error) if the target user is an
// admin and is the only non-banned admin left.
func (h *Handler) wouldRemoveLastAdmin(w http.ResponseWriter, r *http.Request) bool {
	ctx := r.Context()
	target, err := h.app.Repo.GetUser(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return true
	}
	if target.IsAdmin() && !target.Banned {
		n, err := h.app.Repo.CountAdmins(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "admin count failed")
			return true
		}
		if n <= 1 {
			writeError(w, http.StatusBadRequest, "cannot remove the last admin")
			return true
		}
	}
	return false
}

// --- sync queue ---

// adminListSyncJobs returns recent sync jobs across all users for the admin
// queue panel (active jobs first).
func (h *Handler) adminListSyncJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.app.Repo.ListRecentJobs(r.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if jobs == nil {
		jobs = []models.AdminSyncJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// adminRequeueSyncJob returns a job to the queue. Its main use is recovering a
// job wedged in 'running' (worker killed mid-slice) without redeploying: the
// worker reclaims it on the next claim. Reusing RequeueJob also stamps
// last_sliced_at so the round-robin ordering stays sane.
func (h *Handler) adminRequeueSyncJob(w http.ResponseWriter, r *http.Request) {
	if err := h.app.Repo.RequeueJob(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, "requeue failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

// adminArchiveStats returns per-user archive totals (total / archived /
// unarchived) for the admin per-user backup-status panel.
func (h *Handler) adminArchiveStats(w http.ResponseWriter, r *http.Request) {
	users, err := h.app.Repo.ListUserArchiveStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	if users == nil {
		users = []models.UserArchiveStat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// adminSyncFailures returns failure diagnostics: which doc types aren't being
// archived, and the most common sync-item error messages.
func (h *Handler) adminSyncFailures(w http.ResponseWriter, r *http.Request) {
	byType, err := h.app.Repo.UnarchivedByType(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	byError, err := h.app.Repo.SyncFailureReasons(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	bySkipped, err := h.app.Repo.SyncSkippedReasons(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	if byType == nil {
		byType = []models.TypeStat{}
	}
	if byError == nil {
		byError = []models.FailureReason{}
	}
	if bySkipped == nil {
		bySkipped = []models.FailureReason{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"by_type": byType, "by_error": byError, "by_skipped": bySkipped})
}

// --- connections ---

func (h *Handler) adminListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := h.app.Repo.ListConnections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if conns == nil {
		conns = []models.Connection{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": conns})
}

// adminProviderTypes lists the provider implementation types the build supports,
// so the admin connection form can offer them in a dropdown.
func (h *Handler) adminProviderTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"types": provider.FactoryTypes()})
}

type connectionBody struct {
	Type      string `json:"provider_type"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	Domain    string `json:"domain"`
}

func (h *Handler) adminCreateConnection(w http.ResponseWriter, r *http.Request) {
	var b connectionBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if b.Type == "" {
		b.Type = "feishu"
	}
	if !provider.HasFactory(b.Type) {
		writeError(w, http.StatusBadRequest, "unknown provider_type")
		return
	}
	if b.Key == "" || b.AppID == "" || b.AppSecret == "" {
		writeError(w, http.StatusBadRequest, "key, app_id and app_secret are required")
		return
	}
	// domain is a Feishu-specific variant (feishu/lark); default it only there.
	if b.Domain == "" && b.Type == "feishu" {
		b.Domain = "feishu"
	}
	if b.Label == "" {
		b.Label = b.Key
	}
	secretEnc, err := h.app.Cipher.Encrypt(b.AppSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt failed")
		return
	}
	if err := h.app.Repo.CreateConnection(r.Context(), b.Type, b.Key, b.Label, b.AppID, b.Domain, secretEnc); err != nil {
		writeError(w, http.StatusConflict, "create failed (duplicate key?)")
		return
	}
	h.reloadProviders(r)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *Handler) adminUpdateConnection(w http.ResponseWriter, r *http.Request) {
	var b connectionBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// Empty app_secret means "keep the existing one".
	var secretEnc *string
	if b.AppSecret != "" {
		enc, err := h.app.Cipher.Encrypt(b.AppSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt failed")
			return
		}
		secretEnc = &enc
	}
	if err := h.app.Repo.UpdateConnection(r.Context(), r.PathValue("id"), b.Label, b.AppID, b.Domain, secretEnc); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.reloadProviders(r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) adminDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.app.Repo.DeleteConnection(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.reloadProviders(r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) reloadProviders(r *http.Request) {
	if err := h.app.ReloadProviders(r.Context()); err != nil {
		h.app.Log.Error("reload providers failed", "err", err)
	}
}
