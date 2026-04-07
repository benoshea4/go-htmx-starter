package users

import (
	"go-htmx-starter/internal/auth"
	"go-htmx-starter/internal/database"
	"go-htmx-starter/internal/mailer"
	"go-htmx-starter/internal/render"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	Queries       *database.Queries
	Keys          *auth.Keys
	Pool          *pgxpool.Pool
	SecureCookies bool
	Mailer        *mailer.Mailer
}

// ── Page renderers ────────────────────────────────────────────────────────────

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	render.Template(w, r, "login.html", nil)
}

func (h *Handler) SignupPage(w http.ResponseWriter, r *http.Request) {
	render.Template(w, r, "signup.html", nil)
}

func (h *Handler) ForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	render.Template(w, r, "forgot-password.html", nil)
}

func (h *Handler) ResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Prevent browsers and proxies caching this page — it contains the reset token.
	w.Header().Set("Cache-Control", "no-store")
	render.Template(w, r, "reset-password.html", map[string]interface{}{"Token": token})
}

// ── Signup ────────────────────────────────────────────────────────────────────

func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	// Validate email
	if len(email) > 254 {
		sendError(w, "Email address is too long")
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		sendError(w, "Invalid email address")
		return
	}

	// Validate password length — max checked before hashing to prevent Argon2 DoS
	if len(password) < 8 || len(password) > 128 {
		sendError(w, "Password must be between 8 and 128 characters")
		return
	}

	// Hash password
	hash, err := auth.HashPassword(password)
	if err != nil {
		sendError(w, "Server error — please try again")
		return
	}

	// Create user
	user, err := h.Queries.CreateUser(r.Context(), database.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
	})
	if err != nil {
		// Email already taken — don't leak which emails exist via different error messages
		sendError(w, "Could not create account — email may already be registered")
		return
	}

	// Issue token pair and set cookies
	if err := h.issueTokenPair(w, r, user); err != nil {
		sendError(w, "Server error — please try again")
		return
	}

	// Redirect to deals dashboard
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

// ── Login ─────────────────────────────────────────────────────────────────────

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	// Reject obviously invalid inputs early — prevents Argon2 DoS and unnecessary DB hits
	if len(email) > 254 || len(password) > 128 {
		sendError(w, "Invalid email or password")
		return
	}

	// Look up user — same error message whether email exists or not (prevents enumeration).
	// NormalizeTiming runs a dummy Argon2id derivation so this branch takes the same
	// wall time as a real password comparison, preventing timing-based email enumeration.
	user, err := h.Queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		auth.NormalizeTiming(password)
		sendError(w, "Invalid email or password")
		return
	}

	// Verify password — timing-safe comparison inside ComparePassword
	if err := auth.ComparePassword(password, user.PasswordHash); err != nil {
		sendError(w, "Invalid email or password")
		return
	}

	// Issue token pair
	if err := h.issueTokenPair(w, r, user); err != nil {
		sendError(w, "Server error — please try again")
		return
	}

	// Piggyback cleanup — keeps DB lean, fine for MVP traffic levels
	// Move to pg_cron when login latency becomes noticeable (tens of thousands of rows)
	h.Queries.DeleteExpiredRefreshTokens(r.Context())
	h.Queries.DeleteExpiredPasswordResets(r.Context())

	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

// ── Logout ────────────────────────────────────────────────────────────────────

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	// Revoke refresh token in DB — clearing the cookie alone is insufficient.
	// A captured token stays valid until expiry without DB revocation.
	refreshCookie, err := r.Cookie("refresh_token")
	if err == nil {
		rawToken, err := auth.TokenFromString(refreshCookie.Value)
		if err == nil {
			hash := auth.HashToken(rawToken)
			existing, err := h.Queries.GetRefreshTokenByHash(r.Context(), hash)
			if err == nil {
				h.Queries.RevokeRefreshToken(r.Context(), existing.ID)
			}
		}
	}

	// Clear both cookies — Path must match exactly what was set
	for _, name := range []string{"access_token", "refresh_token"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   h.SecureCookies,
			SameSite: http.SameSiteStrictMode,
			Path:     "/",
		})
	}

	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

// ── Settings / Sessions ───────────────────────────────────────────────────────

type sessionView struct {
	database.RefreshToken
	IsCurrent bool
}

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	parsed, err := uuid.Parse(auth.GetUserID(r))
	if err != nil {
		http.Error(w, "session error", http.StatusUnauthorized)
		return
	}
	userID := pgtype.UUID{Bytes: parsed, Valid: true}

	tokens, err := h.Queries.ListActiveSessionsForUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "could not load sessions", http.StatusInternalServerError)
		return
	}

	// Identify the current session by comparing the cookie's token hash
	currentHash := ""
	if c, err := r.Cookie("refresh_token"); err == nil {
		if raw, err := auth.TokenFromString(c.Value); err == nil {
			currentHash = auth.HashToken(raw)
		}
	}

	sessions := make([]sessionView, len(tokens))
	for i, t := range tokens {
		sessions[i] = sessionView{t, t.TokenHash == currentHash}
	}

	render.Template(w, r, "settings.html", map[string]interface{}{
		"Sessions": sessions,
	})
}

func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	parsedToken, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	tokenID := pgtype.UUID{Bytes: parsedToken, Valid: true}

	parsedUser, err := uuid.Parse(auth.GetUserID(r))
	if err != nil {
		http.Error(w, "session error", http.StatusUnauthorized)
		return
	}
	userID := pgtype.UUID{Bytes: parsedUser, Valid: true}

	h.Queries.RevokeSessionForUser(r.Context(), database.RevokeSessionForUserParams{
		ID:     tokenID,
		UserID: userID,
	})

	w.WriteHeader(http.StatusOK)
}

// ── Forgot Password ───────────────────────────────────────────────────────────

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))

	// Always return the same response — prevents email enumeration
	successMsg := "If that email is registered, you'll receive a reset link shortly."

	user, err := h.Queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		// User not found — return identical success message, do nothing else
		w.Write([]byte(successMsg))
		return
	}

	// Generate reset token — raw goes in the link, hash goes in the DB
	rawToken, hash, err := auth.GenerateSecureToken()
	if err != nil {
		w.Write([]byte(successMsg)) // Don't leak server errors
		return
	}

	_, err = h.Queries.InsertPasswordReset(r.Context(), database.InsertPasswordResetParams{
		UserID:    user.ID,
		TokenHash: hash,
	})
	if err != nil {
		w.Write([]byte(successMsg))
		return
	}

	if err := h.Mailer.SendPasswordReset(email, auth.TokenToString(rawToken)); err != nil {
		w.Write([]byte(successMsg)) // Don't leak mail errors
		return
	}

	w.Write([]byte(successMsg))
}

// ── Reset Password ────────────────────────────────────────────────────────────

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.FormValue("token")
	newPassword := r.FormValue("password")

	if len(newPassword) < 8 || len(newPassword) > 128 {
		sendError(w, "Password must be between 8 and 128 characters")
		return
	}

	// Decode hex token string → bytes → hash for DB lookup
	rawToken, err := auth.TokenFromString(tokenStr)
	if err != nil {
		sendError(w, "Invalid or expired reset link")
		return
	}
	tokenHash := auth.HashToken(rawToken)

	// Look up reset record — query checks used=false and expiry automatically
	reset, err := h.Queries.GetPasswordResetByHash(r.Context(), tokenHash)
	if err != nil {
		sendError(w, "Invalid or expired reset link")
		return
	}

	// Hash new password
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		sendError(w, "Server error — please try again")
		return
	}

	// All three DB operations in a single transaction:
	// mark token used → update password → revoke all sessions
	// A crash between any of these would leave the system in a broken state without a transaction.
	err = pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		qtx := h.Queries.WithTx(tx)

		// Mark token used first — prevents reuse even if subsequent steps fail
		if err := qtx.MarkPasswordResetUsed(r.Context(), reset.ID); err != nil {
			return err
		}

		// Update password
		if err := qtx.UpdateUserPassword(r.Context(), database.UpdateUserPasswordParams{
			ID:           reset.UserID,
			PasswordHash: newHash,
		}); err != nil {
			return err
		}

		// Global logout — revoke all refresh tokens for this user (all devices)
		return qtx.DeleteAllRefreshTokensForUser(r.Context(), reset.UserID)
	})

	if err != nil {
		sendError(w, "Server error — please try again")
		return
	}

	// Redirect to login — user must sign in with new password
	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// issueTokenPair generates an access + refresh token and sets both cookies.
func (h *Handler) issueTokenPair(w http.ResponseWriter, r *http.Request, user database.User) error {
	userIDStr := pgUUIDToString(user.ID)

	// Sign access token (JWT, 15 min)
	accessToken, err := h.Keys.NewAccessToken(userIDStr, user.Email)
	if err != nil {
		return err
	}

	// Generate refresh token (random bytes, 7 days)
	rawRefresh, _, err := auth.GenerateSecureToken()
	if err != nil {
		return err
	}
	refreshHash := auth.HashToken(rawRefresh)

	// Store refresh token in DB
	_, err = h.Queries.InsertRefreshToken(r.Context(), database.InsertRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(7 * 24 * time.Hour), Valid: true},
		UserAgent: pgtype.Text{String: r.UserAgent(), Valid: true},
		IpAddress: pgtype.Text{String: r.RemoteAddr, Valid: true},
	})
	if err != nil {
		return err
	}

	// Set access token cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		MaxAge:   int((15 * time.Minute).Seconds()),
		HttpOnly: true,
		Secure:   h.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})

	// Set refresh token cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    auth.TokenToString(rawRefresh),
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   h.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})

	return nil
}

// sendError sets HX-Retarget so HTMX puts the error in #error div, not the main target.
func sendError(w http.ResponseWriter, msg string) {
	w.Header().Set("HX-Retarget", "#error")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.WriteHeader(http.StatusUnprocessableEntity)
	w.Write([]byte(msg))
}

// pgUUIDToString converts pgtype.UUID to a hyphenated UUID string.
func pgUUIDToString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
