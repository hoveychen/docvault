package api

import (
	"context"
	"net/http"

	"github.com/hoveychen/docvault/internal/auth"
	"github.com/hoveychen/docvault/internal/models"
)

type ctxKey string

const (
	userIDKey ctxKey = "user_id"
	userKey   ctxKey = "user"
)

// requireUser enforces a valid session, rejects banned users, and injects the
// user (and id) into the request context.
func (h *Handler) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(sessionCookie)
		if err != nil || ck.Value == "" {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		userID, err := h.app.Sessions.Verify(ck.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid session")
			return
		}
		user, err := h.app.Repo.GetUser(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "account not found")
			return
		}
		if user.Banned {
			writeError(w, http.StatusForbidden, "account is banned")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		ctx = context.WithValue(ctx, userKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin is requireUser plus an admin-role check.
func (h *Handler) requireAdmin(next http.HandlerFunc) http.Handler {
	return h.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if u := userFrom(r); u == nil || !u.IsAdmin() {
			writeError(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func userIDFrom(r *http.Request) string {
	if v, ok := r.Context().Value(userIDKey).(string); ok {
		return v
	}
	return ""
}

func userFrom(r *http.Request) *models.User {
	if v, ok := r.Context().Value(userKey).(*models.User); ok {
		return v
	}
	return nil
}

func cookieMaxAge() int { return auth.CookieMaxAge() }

// withCORS allows the Vite dev server to call the API with credentials during
// local development. In production the frontend is served same-origin.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
