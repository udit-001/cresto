.PHONY: build css dev start stop test vet fmt clean tailwind-download \
        goose-install migrate-up migrate-down migrate-status migrate-create \
        package-extension package-extension-firefox package-extension-chrome

# Load secrets (AMO credentials) if .env exists.
-include .env

# Build the binary (rebuilds CSS first so the embed is fresh).
build: css
	mkdir -p bin
	go build -o bin/cresto .

# Compile Tailwind utilities from web/input.css → internal/web/static/app.css.
# Scans Go files and HTML templates so only used classes ship in the embed.
css:
	go run . tailwind build

# Fetch the Tailwind CLI binary (run once per checkout).
tailwind-download:
	go run . tailwind download

# Start the web UI in the background (daemon). Logs to ~/.cresto/server.log.
start: css
	go run . start

# Stop the daemon.
stop:
	go run . stop

# Run the server in the foreground (development). TODO: add watcher.
dev: css
	go run . start --foreground

# Run all tests.
test:
	go test ./...

# go vet across the module.
vet:
	go vet ./...

# Format Go source.
fmt:
	gofmt -s -w .

# Lint + test in one call.
check: vet test

# Build + install binary to PATH (~/go/bin).
install: build
	cp bin/cresto ~/go/bin/cresto

# Remove build artifacts.
clean:
	rm -rf bin/

# Tidy module deps.
tidy:
	go mod tidy

# --- Goose migrations ---
# Migrations are embedded in the binary and run automatically on 'start'.
# These targets use the built-in 'migrate' command for manual control.

# Run pending migrations.
migrate-up:
	go run . migrate up

# Roll back the most recent migration.
migrate-down:
	go run . migrate down

# Show applied/pending migration versions.
migrate-status:
	go run . migrate status

# --- Migration file creation (uses external goose CLI) ---

MIGRATE_DIR = internal/store/migrations

# Install the goose CLI binary (one-time, for creating migration files).
goose-install:
	go install github.com/pressly/goose/v3/cmd/goose@latest

# Create a new migration file. Usage: make migrate-create name=add_column
migrate-create:
	goose -dir $(MIGRATE_DIR) create $(name) sql

# --- Extension packaging ---
# Firefox: signs via AMO (unlisted = self-distributed, not public).
# Chrome: packages source as a ZIP (load unpacked after extracting).
# Both artifacts are committed into internal/web/extension/ so go build
# embeds them. Run `make package-extension` after any extension change.
#
# Firefox requires AMO credentials in .env (gitignored). Get yours at:
# https://addons.mozilla.org/developers/addon/api/key/
package-extension-firefox:
	@test -n "$(AMO_KEY)" || (echo "AMO_KEY not set — create .env (see .gitignore)" && false)
	@test -n "$(AMO_SECRET)" || (echo "AMO_SECRET not set — create .env (see .gitignore)" && false)
	npx web-ext sign --channel=unlisted --source-dir=extension \
		--api-key=$(AMO_KEY) --api-secret=$(AMO_SECRET)
	cp web-ext-artifacts/*.xpi internal/web/extension/cresto-greythr-connector.xpi
	@echo "Firefox XPI copied to internal/web/extension/"

package-extension-chrome:
	cd extension && zip -r ../internal/web/extension/cresto-connector-chrome.zip \
		manifest.json popup.html popup.js content.js kite-callback.js \
		icon16.png icon48.png icon128.png
	@echo "Chrome ZIP copied to internal/web/extension/"

package-extension: package-extension-firefox package-extension-chrome
	@echo "Extension packaged for Firefox (XPI) and Chrome (ZIP)"
