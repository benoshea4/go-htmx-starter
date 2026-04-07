package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go-htmx-starter/internal/auth"
	"go-htmx-starter/internal/database"
	"go-htmx-starter/internal/mailer"
	"go-htmx-starter/internal/migrations"
	"go-htmx-starter/internal/render"
	"go-htmx-starter/internal/users"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
)

func main() {
	godotenv.Load()

	// ── Structured logging ─────────────────────────────────────────────────────
	if os.Getenv("ENV") == "production" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	}

	// ── Ed25519 keys — auto-generate on first run, reuse on subsequent starts ─
	// Dev:  written to .env automatically, persists across restarts.
	// Prod: set ED25519_PRIVATE_KEY + ED25519_PUBLIC_KEY as server env vars.
	//       Generate fresh keys for prod — never copy dev keys to production.
	if os.Getenv("ED25519_PRIVATE_KEY") == "" || os.Getenv("ED25519_PUBLIC_KEY") == "" {
		if os.Getenv("ENV") == "production" {
			slog.Error("ED25519_PRIVATE_KEY / ED25519_PUBLIC_KEY must be set in production")
			os.Exit(1)
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			slog.Error("failed to generate Ed25519 keys", "err", err)
			os.Exit(1)
		}
		privB64 := base64.StdEncoding.EncodeToString(priv)
		pubB64 := base64.StdEncoding.EncodeToString(pub)

		f, err := os.OpenFile(".env", os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			slog.Error("could not open .env to write keys", "err", err)
			os.Exit(1)
		}
		f.WriteString("\nED25519_PRIVATE_KEY=" + privB64 + "\nED25519_PUBLIC_KEY=" + pubB64 + "\n")
		f.Close()

		os.Setenv("ED25519_PRIVATE_KEY", privB64)
		os.Setenv("ED25519_PUBLIC_KEY", pubB64)
		slog.Info("generated new Ed25519 keys and saved to .env")
	}

	keys, err := auth.LoadKeys(os.Getenv("ED25519_PRIVATE_KEY"), os.Getenv("ED25519_PUBLIC_KEY"))
	if err != nil {
		slog.Error("failed to load Ed25519 keys", "err", err)
		os.Exit(1)
	}

	// ── Database ──────────────────────────────────────────────────────────────
	dbConfig, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		slog.Error("database: failed to parse DATABASE_URL", "err", err)
		os.Exit(1)
	}
	dbConfig.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(context.Background(), dbConfig)
	if err != nil {
		slog.Error("database: connection failed — check DATABASE_URL and PostgreSQL status", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// ── Migrations ────────────────────────────────────────────────────────────
	// SQL files are embedded in the binary — no external files needed on the host.
	db := stdlib.OpenDBFromPool(pool)
	goose.SetBaseFS(migrations.MigrationFiles)
	if err := goose.SetDialect("postgres"); err != nil {
		slog.Error("migrations: failed to set dialect", "err", err)
		os.Exit(1)
	}
	if err := goose.Up(db, "schema"); err != nil {
		slog.Error("migrations: failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations up to date")

	// ── Shared state ──────────────────────────────────────────────────────────
	secureCookies := os.Getenv("ENV") == "production"
	queries := database.New(pool)
	mail := mailer.New(os.Getenv("RESEND_API_KEY"), os.Getenv("APP_URL"))

	// ── Background cleanup ────────────────────────────────────────────────────
	// Deletes expired refresh tokens and password resets for DB hygiene.
	// Expired rows are already rejected at query time; this is purely cosmetic.
	cleanup := func() {
		if err := queries.DeleteExpiredRefreshTokens(context.Background()); err != nil {
			slog.Error("cleanup: failed to delete expired refresh tokens", "err", err)
		}
		if err := queries.DeleteExpiredPasswordResets(context.Background()); err != nil {
			slog.Error("cleanup: failed to delete expired password resets", "err", err)
		}
		slog.Info("cleanup: expired tokens purged")
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanup()
		}
	}()

	// ── Handlers ──────────────────────────────────────────────────────────────
	userHandler := &users.Handler{
		Queries:       queries,
		Keys:          keys,
		Pool:          pool,
		SecureCookies: secureCookies,
		Mailer:        mail,
	}

	// ── Middleware ────────────────────────────────────────────────────────────
	authMiddleware := auth.NewMiddleware(keys, queries, pool, secureCookies)
	// LimitByRealIP reads X-Real-IP set by Caddy from CF-Connecting-IP.
	authLimiter := httprate.LimitByRealIP(5, time.Minute)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()

	// Limit request body to 64KB — well above any legitimate form submission.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
			next.ServeHTTP(w, r)
		})
	})

	// Static files — noDirListFS prevents directory listing of /static/.
	fs := http.FileServer(noDirListFS{http.Dir("web/static")})
	r.Handle("/static/*", http.StripPrefix("/static/", fs))

	// Health
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	// Auth routes — rate limited
	r.With(authLimiter).Post("/signup", userHandler.Signup)
	r.With(authLimiter).Post("/login", userHandler.Login)
	r.With(authLimiter).Post("/forgot-password", userHandler.ForgotPassword)
	r.With(authLimiter).Post("/reset-password", userHandler.ResetPassword)
	r.Post("/logout", userHandler.Logout)

	// Auth pages
	r.Get("/login", userHandler.LoginPage)
	r.Get("/signup", userHandler.SignupPage)
	r.Get("/forgot-password", userHandler.ForgotPasswordPage)
	r.Get("/reset-password", userHandler.ResetPasswordPage)

	// Protected routes — require valid session
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware.Require)
		r.Use(auth.RequireHXRequest)

		// Dashboard — replace with your app's main view
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			render.Template(w, r, "dashboard.html", nil)
		})

		r.Get("/settings", userHandler.SettingsPage)
		r.Delete("/sessions/{id}", userHandler.RevokeSession)
	})

	// ── Start ─────────────────────────────────────────────────────────────────
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("server starting", "addr", "http://localhost:"+port)
	if err = http.ListenAndServe(":"+port, r); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
