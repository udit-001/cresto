# Cresto — Project Reference (formerly `income-tracker`)

## Brand

- **Name**: Cresto (from Latin *crescere* — to grow, increase)
- **Metaphor**: Your income grows, your wealth grows, your financial understanding grows
- **Naming basis**: Personal financial sovereignty — complete, private ownership and control over your financial life

## Stack

- **Go 1.26.5** monolith, server-rendered HTML — NOT React/Next.js/SPA
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO)
- **`html/template`** — Go templates, no client framework
- **Tailwind CSS v4** + **Basecoat** component library
- **Chart.js** for charts, **LM Studio** (local) for LLM vision extraction

## Build & run

```sh
make build        # rebuild CSS + compile → bin/cresto
make dev          # rebuild CSS + start --foreground (hot-reload TODO: add watcher)
make start        # rebuild CSS + start server as a daemon
make stop         # stop the daemon
make test         # go test ./...
make migrate-up   # run pending goose migrations (auto-runs on start too)
make migrate-down # roll back the most recent migration
make migrate-status  # show migration version
make migrate-create name=<desc>  # create a new migration file (needs goose CLI)
make goose-install   # install goose CLI binary (for migrate-create only)
go build -o ~/go/bin/cresto .   # install binary to PATH
```

Data lives at `~/.cresto/` (income.db + payslips/).
PID file at `~/.config/cresto/server.pid`, daemon logs at `~/.cresto/server.log`.

## Server commands

- `start` — daemon (production). Detaches from the terminal. Use `--foreground`/`-f` for development (blocks terminal, logs to stdout).
- `stop` — stop the daemon. Health-checks via the PID file before killing; cleans up stale files.

## Project layout

```
internal/
  store/           # SQLite persistence: migrations, queries, Filter struct
    migrations/    # Goose versioned SQL migrations (embedded)  
  web/             # HTTP server, handlers, templates, static assets
  llm/             # LLM vision client for PDF extraction
  config/          # Config struct, defaults
  pdfstore/        # PDF file storage
  render/          # PDF → PNG conversion
  tailwind/        # Tailwind CLI download + build
```

## Template gotchas

- Templates live in `internal/web/templates/` and are embedded with `//go:embed`
- Custom template funcs registered in `server.go:tmplFuncs`:
  `money`, `monthName`, `monthShort`, `periodLabel`, `json`, `urlquery`, `abs`, `inc`, `dec`, `sparklineSVG`, `yoySlopegraphSVG`
- Every page template receives a `pageData` struct (Title, PendingCount, Active, Breadcrumbs)
- Static files in `internal/web/static/` are also embedded — need `go build` to pick up changes

## Basecoat component library

- CSS: `/static/basecoat.cdn.min.css` (vega style)
- JS: `/static/all.min.js` (Basecoat all-in-one bundle, NOT Font Awesome)
- Basecoat select uses custom HTML structure, NOT native `<select>`:
  ```html
  <div class="select" data-placeholder="...">
    <button type="button" aria-haspopup="listbox" ... aria-controls="...">
      <span class="truncate">...</span>
      <svg>chevron-down</svg>
    </button>
    <div data-popover aria-hidden="true">
      <div role="listbox" ...>
        <div role="option" data-value="...">Option</div>
      </div>
    </div>
    <input type="hidden" name="..." value="">
  </div>
  ```
- Dispatches `change` event on `.select` root with `event.detail.value`
- Always check the Basecoat docs at https://basecoatui.com/llms.txt before rolling custom HTML. For any component referenced there, load its page with `.md` suffix (e.g. `https://basecoatui.com/components/select.md`)
- Basecoat JS API: call `document.getElementById('toaster').toast({ category, title, description })` — category is "success" | "error" | "info" | "warning". Requires a `<div id="toaster" class="toaster">` element (present in layout.html).

## Data model gotchas

- **Company is NOT a separate table.** `employer_name TEXT` on `payslips` — free text, no foreign key, no normalization. Use `SELECT DISTINCT employer_name` to list companies. Elevating to a first-class object is a provisional future bet.
- `store.Filter` struct supports many unexposed fields (Status, Employer, YearFrom/To, MonthFrom/To, BatchID) — not all have UI
- Status flow: `processing → pending_review → confirmed` (or `→ failed`)
- Payslip components use canonical names (`canonicals` table) for cross-payslip aggregation

## Common mistakes to avoid

- Don't assume a JS framework — it's all server-rendered Go templates
- Don't edit static files without rebuilding — they're `embed.FS`, not hot-reloaded
- Don't create a `companies` table without explicit need — employer_name is sufficient for filtering
- Don't use native `<select>` — use Basecoat select component
- Don't add new template funcs without registering them in `tmplFuncs` (server.go)
- Don't add Font Awesome — Basecoat uses inline Lucide SVGs for icons
- Don't edit `schema.sql` — it's gone. Schema changes go in `internal/store/migrations/` as numbered SQL files with `-- +goose Up` / `-- +goose Down` markers. Run `make migrate-create name=<desc>` to add one.
