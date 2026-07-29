.PHONY: build css dev start stop test vet fmt clean tailwind-download \
        goose-install migrate-up migrate-down migrate-status migrate-create

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
