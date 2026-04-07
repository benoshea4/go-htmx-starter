.PHONY: run build build-linux deploy migrate migrate-status migrate-down seed generate css css-watch dev tailwind-install setup

# Production server — override with: make deploy SERVER=root@yourserver.com
SERVER ?= root@yourserver

TAILWIND_VERSION ?= v4.2.1

# File target: only runs when bin/tailwindcss is missing.
# To upgrade, change TAILWIND_VERSION and run: make tailwind-install
bin/tailwindcss:
	@mkdir -p bin
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ARCH=$$(uname -m); \
	if [ "$$OS" = "darwin" ] && [ "$$ARCH" = "arm64" ]; then BINARY="tailwindcss-macos-arm64"; \
	elif [ "$$OS" = "darwin" ]; then BINARY="tailwindcss-macos-x64"; \
	elif [ "$$ARCH" = "aarch64" ]; then BINARY="tailwindcss-linux-arm64"; \
	else BINARY="tailwindcss-linux-x64"; fi; \
	echo "Downloading Tailwind CSS $(TAILWIND_VERSION) ($$BINARY)..."; \
	curl -sLo bin/tailwindcss https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/$$BINARY; \
	chmod +x bin/tailwindcss

setup:
	@bash scripts/setup.sh

tailwind-install:
	@rm -f bin/tailwindcss
	@$(MAKE) bin/tailwindcss

css: bin/tailwindcss
	./bin/tailwindcss -i web/input.css -o web/static/css/tailwind.css --minify

css-watch: bin/tailwindcss
	./bin/tailwindcss -i web/input.css -o web/static/css/tailwind.css --watch

dev: setup
	@trap 'kill 0' INT TERM; \
	./bin/tailwindcss -i web/input.css -o web/static/css/tailwind.css --watch & \
	air

run: setup
	go run ./cmd/api

build: css
	go build -o bin/app ./cmd/api

# Cross-compile for linux/amd64 (e.g. Hetzner VPS). Used for deploys from macOS.
build-linux: css
	GOOS=linux GOARCH=amd64 go build -o bin/app ./cmd/api

deploy: build-linux
	rsync -av bin/app $(SERVER):/opt/app/
	rsync -av --exclude='input.css' web/ $(SERVER):/opt/app/web/
	ssh $(SERVER) systemctl restart app

# The app auto-runs migrations on startup; these targets are for manual
# inspection and rollback in dev or on the VPS when needed.
migrate:
	goose -dir internal/migrations/schema postgres "$(DATABASE_URL)" up

migrate-status:
	goose -dir internal/migrations/schema postgres "$(DATABASE_URL)" status

# Rolls back the most recent migration one step.
migrate-down:
	goose -dir internal/migrations/schema postgres "$(DATABASE_URL)" down

seed:
	go run ./cmd/seed

generate:
	sqlc generate
