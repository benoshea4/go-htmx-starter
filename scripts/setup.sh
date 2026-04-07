#!/usr/bin/env bash
# setup.sh — one-command dev environment setup for go-htmx-starter
# Usage: bash scripts/setup.sh
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'
info()  { echo -e "${GREEN}==>${NC} ${BOLD}$1${NC}"; }
warn()  { echo -e "${YELLOW}warning:${NC} $1"; }
error() { echo -e "${RED}error:${NC} $1"; exit 1; }
ok()    { echo -e "${GREEN}ok${NC}    $1"; }

OS=$(uname -s)

# ── 1. Go ─────────────────────────────────────────────────────────────────────
info "Checking Go..."
command -v go &>/dev/null || error "Go is not installed. Download it at https://golang.org/dl then re-run this script."
ok "$(go version)"

# ── 2. Air ────────────────────────────────────────────────────────────────────
info "Checking Air (hot reload)..."
if command -v air &>/dev/null; then
    ok "Air already installed"
else
    echo "  Installing Air..."
    go install github.com/air-verse/air@latest
    ok "Air installed"
fi

# ── 3. PostgreSQL ─────────────────────────────────────────────────────────────
info "Checking PostgreSQL..."
if command -v psql &>/dev/null; then
    ok "$(psql --version)"
else
    if [ "$OS" = "Darwin" ]; then
        command -v brew &>/dev/null || error "PostgreSQL not found and Homebrew is not installed.\nInstall Homebrew (https://brew.sh) or PostgreSQL manually, then re-run."
        echo "  Installing PostgreSQL via Homebrew..."
        brew install postgresql
        brew services start postgresql
        ok "PostgreSQL installed"
    elif [ "$OS" = "Linux" ]; then
        if command -v apt-get &>/dev/null; then
            echo "  Installing PostgreSQL via apt..."
            sudo apt-get update -q
            sudo apt-get install -y postgresql postgresql-contrib
            sudo systemctl enable --now postgresql
            ok "PostgreSQL installed"
        else
            error "PostgreSQL not found. Install it manually for your distro then re-run."
        fi
    else
        error "Unsupported OS: $OS. Install PostgreSQL manually then re-run."
    fi
fi

# ── 4. Start PostgreSQL if not running ────────────────────────────────────────
info "Ensuring PostgreSQL is running..."
if pg_isready -q 2>/dev/null; then
    ok "PostgreSQL is accepting connections"
else
    warn "PostgreSQL is not ready — attempting to start..."
    if [ "$OS" = "Darwin" ] && command -v brew &>/dev/null; then
        brew services start postgresql 2>/dev/null || true
    elif [ "$OS" = "Linux" ]; then
        sudo systemctl start postgresql 2>/dev/null || true
    fi
    # Wait up to 10s
    for i in $(seq 1 10); do
        pg_isready -q 2>/dev/null && break
        sleep 1
    done
    pg_isready -q 2>/dev/null || error "PostgreSQL did not start. Check your installation."
    ok "PostgreSQL started"
fi

# ── 5. Create database ────────────────────────────────────────────────────────
info "Checking database..."
# Extract DB name from DATABASE_URL in .env (if present), else fall back to "app"
DB_NAME="app"
if [ -f .env ]; then
    PARSED=$(grep -E '^DATABASE_URL=' .env | sed 's|.*\/\([^?]*\).*|\1|' || true)
    [ -n "$PARSED" ] && DB_NAME="$PARSED"
fi

if psql -lqt 2>/dev/null | cut -d\| -f1 | grep -qw "$DB_NAME"; then
    ok "Database '$DB_NAME' already exists"
else
    echo "  Creating database '$DB_NAME'..."
    # On Linux, Postgres runs as the postgres system user by default
    if [ "$OS" = "Linux" ] && ! psql -c '\q' 2>/dev/null; then
        sudo -u postgres createdb "$DB_NAME"
    else
        createdb "$DB_NAME"
    fi
    ok "Database '$DB_NAME' created"
fi

# ── 6. .env ───────────────────────────────────────────────────────────────────
info "Checking .env..."
if [ -f .env ]; then
    ok ".env already exists — skipping"
else
    cp .env.example .env
    ok ".env created from .env.example — edit DATABASE_URL if needed"
fi

# ── 7. Tailwind CSS binary ────────────────────────────────────────────────────
info "Checking Tailwind CSS binary..."
if [ -f bin/tailwindcss ]; then
    ok "Tailwind binary already present"
else
    echo "  Downloading Tailwind..."
    make tailwind-install
    ok "Tailwind installed"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}Setup complete.${NC}"
echo ""
echo "  Next steps:"
echo "    1. Edit .env and set DATABASE_URL if your Postgres credentials differ"
echo "    2. make dev       — start the app"
echo "    3. make seed      — create the dev user (dev@example.com / devpassword)"
echo ""
