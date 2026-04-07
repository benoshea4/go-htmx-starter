// cmd/seed/main.go — dev-only seeder. Never run against production.
// Usage: go run ./cmd/seed
package main

import (
	"context"
	"go-htmx-starter/internal/auth"
	"go-htmx-starter/internal/database"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const (
	devEmail    = "dev@example.com"
	devPassword = "devpassword"
)

func main() {
	godotenv.Load()

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	q := database.New(pool)

	// Idempotent — skip if already exists
	existing, err := q.GetUserByEmail(context.Background(), devEmail)
	if err == nil {
		fmt.Printf("Dev user already exists: %s (id=%s)\n", existing.Email, existing.ID)
		return
	}

	hash, err := auth.HashPassword(devPassword)
	if err != nil {
		slog.Error("hash password", "err", err)
		os.Exit(1)
	}

	user, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Email:        devEmail,
		PasswordHash: hash,
	})
	if err != nil {
		slog.Error("create user", "err", err)
		os.Exit(1)
	}

	fmt.Printf("Dev user created:\n  email:    %s\n  password: %s\n  id:       %s\n", devEmail, devPassword, user.ID)
}
