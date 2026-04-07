# go-htmx-starter

A minimal, self-hosted Go web app starter. Go backend, HTMX frontend, Postgres storage. Full auth built-in — add your own domain logic on top.

## Stack

| Tool | Role |
|------|------|
| Go + Chi | Backend + routing |
| HTMX | Frontend interactivity (no JS framework) |
| Tailwind CSS v4 | Styling |
| PostgreSQL + pgx | Database |
| sqlc | Type-safe SQL → Go codegen |
| Goose | Migrations (auto-run on startup) |

## Why this stack

**Go** compiles to a single static binary with no runtime, no interpreter, and no dependency to install on the server. `rsync` the binary, restart the process — done. It is readable enough that a new contributor can follow the code without a language reference open, which rules out Rust for most web product work (Rust's strengths are memory layout control and fearless concurrency at the systems level — overkill here, and the learning curve slows teams down).

**HTMX** eliminates the JS build pipeline entirely. No bundler, no transpiler, no `node_modules`, no version conflicts. The server renders HTML; HTMX swaps fragments. For the overwhelming majority of web UIs this is sufficient, and it means the backend and frontend are the same language, the same process, and the same deploy.

**Tailwind v4 standalone CLI** is a single downloaded binary — no Node required. CSS is compiled at build time and served as a static file.

**PostgreSQL** is the one dependency that runs separately, and it is worth it. A real relational database with full ACID guarantees, JSON columns, and a 30-year track record beats every embedded or managed alternative for anything you intend to grow.

**Minimal dependencies** is a deliberate constraint. Every dependency is a future upgrade, a CVE, or a breaking change. The stdlib handles HTTP, templates, crypto, structured logging, and embedding. The packages added beyond that (Chi, pgx, sqlc, Goose, jwt, httprate) each do exactly one thing and have stable APIs.

## Tradeoffs and recommendations

These are the decisions baked into this starter and what you might swap out depending on your needs.

### Caddy instead of nginx

The deploy config uses Caddy. Like Go, Caddy is a single binary — no package manager, no module system, no `sites-enabled` symlinks. It handles TLS automatically via Let's Encrypt with zero configuration. If you already know nginx and have existing config, nginx works fine; the main practical difference is that nginx requires certbot or a separate ACME client to manage certs, while Caddy does it natively.

### Cloudflare Tunnel instead of open ports

The HA deploy uses Cloudflare Tunnel rather than opening ports 80/443 on the server. The server makes an outbound connection to Cloudflare — no inbound firewall rules, no exposed IPs, DDoS mitigation included. The tradeoff is a hard dependency on Cloudflare. If you want full infrastructure independence, open ports and point DNS directly at your server.

### Chi instead of stdlib `net/http` (Go 1.22+)

Go 1.22 added method and wildcard routing to the stdlib. Chi is still useful for middleware chaining and route grouping, but if you want zero dependencies on the router, the stdlib is now a viable choice for straightforward APIs.

### sqlc instead of an ORM

sqlc generates type-safe Go from plain SQL. You write SQL, you get Go functions — no query builder, no reflection, no magic. The tradeoff is that schema changes require re-running `make generate`. If you want to iterate on the schema very quickly in early development, a lightweight query builder like `sq` adds flexibility; an ORM like GORM adds convenience at the cost of obscuring what queries actually run.

### JWT + refresh tokens instead of server-side sessions

This starter uses stateless JWTs (15 min) + hashed refresh tokens stored in Postgres (7 days). The benefit is no session store and easy horizontal scaling. The tradeoff is that access tokens cannot be revoked mid-lifetime — only refresh tokens can be revoked. If immediate revocation is a hard requirement (e.g. financial or healthcare), use server-side sessions backed by Redis or Postgres instead.

### Hetzner instead of AWS/GCP/Azure

The HA deploy targets Hetzner CX23 nodes (~€4/month each). For a self-hosted app the cost difference is significant. The tradeoff is fewer managed services — you own the database, the load balancer, and the networking. If you want managed Postgres, managed Redis, and auto-scaling, a hyperscaler fits better; expect 5–10x the cost.

---

## Quick start

### 1. Tailwind CSS binary

The Tailwind standalone CLI is required to build CSS. It is not included in the repo — download it with:

```bash
make tailwind-install
```

This downloads the correct binary for your OS/arch into `bin/tailwindcss` (gitignored).

> **Alternative (no download):** If you'd prefer not to download the binary, you can switch to the Tailwind CDN play build. See the commented-out option in `web/templates/layout.html` and the `css` / `css-watch` / `dev` targets in the `Makefile`. The CDN build is not recommended for production.

### 2. Environment

```bash
cp .env.example .env
# Edit .env — DATABASE_URL is required; Ed25519 keys are auto-generated on first run
```

### 3. Run

```bash
make dev   # Tailwind watch + Air hot reload
```

App runs at `http://localhost:8080`.

### Dev credentials

```
Email:    dev@example.com
Password: devpassword
```

Run `make seed` to create/reset the dev user.

## Commands

```bash
make dev             # Tailwind watch + Air hot reload (Ctrl+C stops both)
make run             # go run ./cmd/api (no Tailwind watch)
make build           # build CSS then go build
make seed            # create/reset dev user
make generate        # sqlc generate (after editing sql/queries/)
make migrate         # goose up
make migrate-down    # roll back one step
make migrate-status  # show current DB version
make tailwind-install  # download Tailwind binary into bin/
```

## Adding a feature

1. Add a migration in `internal/migrations/schema/` (`YYYYMMDDHHMMSS_description.sql`)
2. Add SQL queries in `internal/database/queries/`
3. Run `make generate`
4. Create a handler in `internal/<feature>/handler.go`
5. Register routes in `cmd/api/main.go`
6. Add templates in `web/templates/`

## What's built in

- Full auth: signup, login, logout, forgot-password, reset-password
- Ed25519 JWT access tokens + hashed refresh tokens with silent rotation
- Argon2id password hashing
- Per-IP rate limiting on all auth routes
- Sessions UI at `/settings` with per-session revoke
- HTMX-aware render layer (full page vs partial swap)
- Structured logging (JSON in prod, text in dev)
- Migrations embedded in binary via `go:embed`
- Hot reload with Air

## Deploy

See `deploy/` for Hetzner + Cloudflare Tunnel + Patroni HA config.
