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
| Resend | Transactional email (stdout fallback in dev) |

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

The only hard prerequisite is **Go** ([golang.org/dl](https://golang.org/dl)).

```bash
make dev     # installs tools, creates DB + .env, starts Tailwind watch + Air
make seed    # create dev@example.com / devpassword
```

App runs at `http://localhost:8080`.

`make dev` and `make run` handle all prerequisites automatically — Air, PostgreSQL, Tailwind binary, database, and `.env`. Re-running is safe; anything already present is skipped.

> **Tailwind CDN alternative:** if you'd prefer not to download the binary, see the commented-out CDN option in `web/templates/layout.html`. Not recommended for production.

## Commands

```bash
make dev             # install tools + create DB/env if needed, then Tailwind watch + Air
make run             # install tools + create DB/env if needed, then go run ./cmd/api
make setup           # same prerequisite checks as dev/run, standalone
make build           # build CSS then go build (native)
make build-linux     # cross-compile for linux/amd64 (used by make deploy)
make seed            # create/reset dev user
make generate        # sqlc generate (after editing internal/database/queries/)
make migrate         # goose up
make migrate-down    # roll back one step
make migrate-status  # show current DB version
make tailwind-install  # (re)download Tailwind binary into bin/
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

### Production environment variables

In production, Ed25519 keys are **not** auto-generated — the app exits on startup if they are missing. Generate them once and set them as persistent server environment variables (not in `.env`):

The easiest way to get production keys is to run `make dev` once in a dev environment — the app auto-generates keys and appends them to `.env`. Copy the `ED25519_PRIVATE_KEY` and `ED25519_PUBLIC_KEY` lines to your server's environment (not `.env` — use systemd `EnvironmentFile`, Docker secrets, or your hosting provider's env config). Rotating these keys logs out all users cluster-wide.

### Static files

The binary serves CSS and JS from the `web/static/` directory on disk — it is not self-contained. `make deploy` rsyncs `web/` to the server automatically. If you deploy manually or containerize the app, ensure `web/static/` is present alongside the binary.

### `make deploy` is single-node

`make deploy` deploys to the single host in `$(SERVER)`. For a multi-node Patroni cluster, use a rolling loop so Cloudflare routes around nodes being restarted:

```bash
make build-linux
for NODE in root@10.0.0.2 root@10.0.0.3 root@10.0.0.4; do
    rsync -av bin/app ${NODE}:/opt/app/
    rsync -av --exclude='input.css' web/ ${NODE}:/opt/app/web/
    ssh ${NODE} "systemctl restart app"
    sleep 5
done
```

### Health endpoint

`GET /health` returns `200 OK` — use this for load balancer and uptime checks.
