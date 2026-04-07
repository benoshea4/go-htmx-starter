package auth

import (
	"context"
	"go-htmx-starter/internal/database"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	ContextKeyUserID contextKey = "user_id"
	ContextKeyEmail  contextKey = "email"
)

type Middleware struct {
	keys          *Keys
	queries       *database.Queries
	pool          *pgxpool.Pool
	secureCookies bool
}

func NewMiddleware(keys *Keys, queries *database.Queries, pool *pgxpool.Pool, secureCookies bool) *Middleware {
	return &Middleware{keys: keys, queries: queries, pool: pool, secureCookies: secureCookies}
}

// Require enforces authentication on any route it wraps.
// 1. Valid access_token  → inject context, continue.
// 2. Expired access_token + valid refresh_token → rotate (in transaction), new cookies, continue.
// 3. Anything else → clear cookies, HX-Redirect /login.
func (m *Middleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// ── 1. Try access token ───────────────────────────────────────────────
		accessCookie, _ := r.Cookie("access_token")
		if accessCookie != nil {
			claims, err := m.keys.ValidateAccessToken(accessCookie.Value)
			if err == nil {
				// JWT is cryptographically valid — but also check the refresh token
				// hasn't been revoked. Without this, a revoked session remains active
				// until the 15-min JWT expires.
				refreshCookie, err := r.Cookie("refresh_token")
				if err != nil {
					m.clearAndRedirect(w, r)
					return
				}
				rawToken, err := TokenFromString(refreshCookie.Value)
				if err != nil {
					m.clearAndRedirect(w, r)
					return
				}
				if _, err := m.queries.GetRefreshTokenByHash(r.Context(), HashToken(rawToken)); err != nil {
					m.clearAndRedirect(w, r)
					return
				}
				ctx := context.WithValue(r.Context(), ContextKeyUserID, claims.Subject)
				ctx = context.WithValue(ctx, ContextKeyEmail, claims.Email)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if !errors.Is(err, jwt.ErrTokenExpired) {
				// Tampered or malformed — reject hard
				m.clearAndRedirect(w, r)
				return
			}
		}

		// ── 2. Try refresh token ──────────────────────────────────────────────
		refreshCookie, err := r.Cookie("refresh_token")
		if err != nil {
			m.clearAndRedirect(w, r)
			return
		}

		rawToken, err := TokenFromString(refreshCookie.Value)
		if err != nil {
			m.clearAndRedirect(w, r)
			return
		}

		existing, err := m.queries.GetRefreshTokenByHash(r.Context(), HashToken(rawToken))
		if err != nil {
			m.clearAndRedirect(w, r)
			return
		}

		userIDStr := pgUUIDToString(existing.UserID)

		// Recover email from expired access token (safe — refresh already validated above)
		userEmail := ""
		if accessCookie != nil {
			if expiredClaims, err := parseUnverifiedClaims(accessCookie.Value); err == nil {
				userEmail = expiredClaims.Email
			}
		}
		if userEmail == "" {
			m.clearAndRedirect(w, r)
			return
		}

		// ── 3. Rotate in a transaction ────────────────────────────────────────
		var newRawRefresh []byte

		err = pgx.BeginFunc(r.Context(), m.pool, func(tx pgx.Tx) error {
			qtx := m.queries.WithTx(tx)

			if err := qtx.RevokeRefreshToken(r.Context(), existing.ID); err != nil {
				return err
			}

			newRaw, newHash, err := GenerateSecureToken()
			if err != nil {
				return err
			}
			newRawRefresh = newRaw

			_, err = qtx.InsertRefreshToken(r.Context(), database.InsertRefreshTokenParams{
				UserID:    existing.UserID,
				TokenHash: newHash,
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(7 * 24 * time.Hour), Valid: true},
				UserAgent: pgtype.Text{String: r.UserAgent(), Valid: true},
				IpAddress: pgtype.Text{String: r.RemoteAddr, Valid: true},
			})
			return err
		})

		if err != nil {
			m.clearAndRedirect(w, r)
			return
		}

		// Issue new access token
		newAccess, err := m.keys.NewAccessToken(userIDStr, userEmail)
		if err != nil {
			m.clearAndRedirect(w, r)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "access_token",
			Value:    newAccess,
			MaxAge:   int(accessTokenDuration.Seconds()),
			HttpOnly: true,
			Secure:   m.secureCookies,
			SameSite: http.SameSiteStrictMode,
			Path:     "/",
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "refresh_token",
			Value:    TokenToString(newRawRefresh),
			MaxAge:   int((7 * 24 * time.Hour).Seconds()),
			HttpOnly: true,
			Secure:   m.secureCookies,
			SameSite: http.SameSiteStrictMode,
			Path:     "/",
		})

		ctx := context.WithValue(r.Context(), ContextKeyUserID, userIDStr)
		ctx = context.WithValue(ctx, ContextKeyEmail, userEmail)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) clearAndRedirect(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"access_token", "refresh_token"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   m.secureCookies,
			SameSite: http.SameSiteStrictMode,
			Path:     "/",
		})
	}
	// HTMX follows 303s automatically and injects the response into the
	// hx-target — send 200 + HX-Redirect so HTMX does a full page navigation.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func pgUUIDToString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseUnverifiedClaims(tokenString string) (*Claims, error) {
	p := jwt.NewParser()
	claims := &Claims{}
	_, _, err := p.ParseUnverified(tokenString, claims)
	return claims, err
}

// GetUserID pulls the authenticated user ID from request context.
func GetUserID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyUserID).(string)
	return v
}

// RequireHXRequest rejects state-mutating requests that don't carry the HX-Request
// header. Browsers cannot set custom headers on cross-origin form submissions,
// so this — combined with SameSite=Strict cookies — closes the CSRF surface.
func RequireHXRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodDelete:
			if r.Header.Get("HX-Request") != "true" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// GetEmail pulls the authenticated user email from request context.
func GetEmail(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyEmail).(string)
	return v
}
