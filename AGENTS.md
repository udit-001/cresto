# Cresto — Project Reference

## Brand

- **Name**: Cresto (from Latin *crescere* — to grow, increase)
- **Metaphor**: Your income grows, your wealth grows, your financial understanding grows
- **Naming basis**: Personal financial sovereignty — complete, private ownership and control over your financial life

## Stack

- **Go 1.26.5** monolith, server-rendered HTML — NOT React/Next.js/SPA
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO)
- **`html/template`** for rendering
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
make package-extension          # package browser extension for Firefox + Chrome
make package-extension-firefox  # Firefox only (needs AMO creds in .env)
make package-extension-chrome   # Chrome only (no creds needed)
go build -o ~/go/bin/cresto .   # install binary to PATH
```

Data lives at `~/.cresto/` (income.db + payslips/ + groww_token.json + kite_session.json + greythr_session.json).
AMO credentials for Firefox extension signing live in `.env` (gitignored).
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
    extension/     # Packaged extension artifacts (XPI + ZIP), embedded via //go:embed
  llm/             # LLM vision client for PDF extraction
  config/          # Config struct, defaults
  pdfstore/        # PDF file storage
  render/          # PDF → PNG conversion
  tailwind/        # Tailwind CLI download + build
  mcp/             # Shared MCP protocol client (JSON-RPC, SSE)
  groww/           # Groww broker adapter (OAuth + holdings)
  kite/            # Kite/Zerodha broker adapter (session auth + holdings/MF/trades)
  greythr/         # greytHR ESS adapter (cookie auth + JSON payslip data)
extension/         # Browser extension source (edit here, package via make)
```

## Template gotchas

- Templates live in `internal/web/templates/` and are embedded with `//go:embed`
- Custom template funcs registered in `server.go:tmplFuncs`:
  `money`, `money2`, `monthName`, `monthShort`, `periodLabel`, `json`, `urlquery`, `abs`, `neg`, `sign`, `inc`, `dec`, `sparklineSVG`, `yoySlopegraphSVG`
- `money` drops trailing `.00`; `money2` always shows 2 decimals (used by broker tables)
- `neg` returns bool (for P&L coloring); `sign` returns "+" or "" (for P&L prefix)
- Go templates can't compare float64 with int literals — use `neg`/`sign` funcs instead of `lt .X 0` / `ge .X 0`
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
- Don't add new template funcs without registering them in `tmplFuncs` (server.go)
- Don't edit `schema.sql` — it's gone. Schema changes go in `internal/store/migrations/` as numbered SQL files with `-- +goose Up` / `-- +goose Down` markers. Run `make migrate-create name=<desc>` to add one.
- Don't introduce a `Broker` interface at the web layer — Groww and Kite have different capabilities (Kite has MF holdings). They're separate concrete dependencies.

For template comparison rules, Basecoat select HTML, and icon conventions, see the sections above.

## Broker MCP integrations

Cresto connects to Groww and Zerodha (Kite) via their free MCP servers for live holdings data.

### Architecture

- `internal/mcp/` — shared MCP protocol client (JSON-RPC 2.0 over Streamable HTTP + SSE). 3 methods: `Initialize`, `ListTools`, `CallTool`. Stateless — caller provides auth headers per call.
- `internal/groww/` — Groww adapter. OAuth 2.1 + PKCE + DCR. Groww's DCR returns a shared client with a fixed `localhost:52155` redirect URI. Transient HTTP listener on that port during auth. Token expires daily (~24h), no refresh. Token at `~/.cresto/groww_token.json`.
- `internal/kite/` — Kite adapter. Session-based auth (initialize → login tool → authorize URL → user authenticates at Zerodha). No OAuth, no callback listener. Session at `~/.cresto/kite_session.json`. `callRaw` internal seam shared by Holdings, MFHoldings, Trades.

### Web UI

- `/groww` — connect/disconnect, equity holdings table with AJAX refresh, disconnect confirmation dialog
- `/kite` — connect/disconnect, equity + MF holdings tables, trades table, AJAX refresh
- Both use `prepareXView` pattern: one method computes the view model, both HTML and JSON handlers serialize it

### CLI commands (for agents)

- `cresto holdings` — live holdings from all connected brokers, `--broker groww|kite`, `--json`. No server/DB required.
- `cresto quote SYMBOL [SYMBOL...]` — LTP + OHLC lookup, `--exchange NSE|BSE` (default NSE), `--broker`, `--json`. Auto-selects Kite (exact match, richer data) over Groww (fuzzy search).

### Limitations

- **No historical trades**: Kite MCP's `get_trades` returns today only. Kite Connect API also lacks historical trades. Tax filing requires manual Console export.
- **No MF on Groww**: Groww MCP's `get_mutualfund_details` is a stub.
- **Groww token expires daily**: reconnect via web UI (`/groww` → Connect).
- **Kite session can become invalid**: detected on next fetch (returns "Invalid session ID" or "Please log in first"). The session file is marked expired — settings and portfolio show "Session expired" with a Reconnect button. The extension auto-redirects back to Cresto after Kite auth completes.
- Broker connections happen through the web UI, not CLI. The CLI reads the saved token/session files.

## greytHR payslip auto-fetch

Cresto fetches payslips from greytHR's Employee Self Service (ESS) portal using cookie-based auth and greytHR's internal JSON APIs. This is a **JSON-first** path — no PDF download or LLM vision extraction needed for data. The PDF is still downloaded for archival so the review UI can display it.

### Architecture

- `internal/greythr/` — ESS adapter. Cookie auth (user pastes `access_token` from browser DevTools, or the extension automates this). API calls:
  - `ListPayslipMonths` → `GET /v3/api/payroll/months/{profile_id}/published?type=payslip` — list of released payslip periods
  - `FetchPayslipData` → `GET /v3/api/payroll/payslip/{profile_id}/{payslip_id}/published` — **full structured payslip data as JSON** (earnings, deductions, net pay, all hierarchical)
  - `DownloadPayslipPDF` → `GET /v3/api/payroll/payslip/{profile_id}/{payslip_id}/download` — PDF for archival
  - `FetchEmployeeInfo` → `GET /core-hr/v1/empandjob/data/{id}` + `GET /v3/api/empinfo/personal/data/{id}` — designation + employee number
  - `FetchYTDSummary` → `GET /v3/api/payroll/ytd-statement-summary/{id}/{fy_year}/0` — per-FY YTD data; `YTDForMonth(month)` sums Apr→target month in FY order
- `greythr.MapToPayslip` converts the JSON response directly to `store.Payslip` — bypasses the render → LLM extract → classify pipeline entirely. Uses a direct name→canonical map (e.g. `BASIC` → `basic`, `PF` → `epf`) with keyword fallback. YTD amounts are set on components from the `ytd` map.
- `greythr.FYYearFor(month, year)` returns the Indian FY start year (Apr→Mar).
- Session at `~/.cresto/greythr_session.json` (0600 perms): host, access_token, profile_id, email.
- PDFs saved with subdomain prefix (e.g. `gyansys_Payslip_Jun_2026.pdf`) for multi-employer disambiguation.

### Web UI

- `/greythr` — connect (paste host + access_token + profile_id, or use the browser extension), disconnect, list available months, fetch button
- `/upload` — greytHR fetch card alongside PDF upload (connected → fetch button; not connected → connect link)
- `/settings` — greytHR connection status card with link to `/greythr`
- Fetch runs as a background batch (same `upload_batches` table + progress page as PDF uploads). Payslips enter as `pending_review`.

### CLI commands

- `cresto greythr fetch` — fetch all unpublished payslips. Reads saved session, dedups against existing DB payslips by period, skips empty (zero-value) months. No server required (reads session file + writes to DB directly).

### Limitations

- **Cookie expires**: greytHR sessions are short-lived (Ory Kratos tokens). Reconnect via web UI (`/greythr` → paste new token).
- **Reverse-engineered API**: uses greytHR's internal ESS endpoints, not the official admin API. Could change with greytHR updates.
- **Single profile**: one greytHR account per Cresto instance (one session file).

## Browser extension

The extension (`extension/`) automates setup for both greytHR and Kite. Packaged artifacts (XPI for Firefox, ZIP for Chrome) are embedded in the binary via `//go:embed extension` and served from `internal/web/extension/`.

### What it does

- **greytHR**: reads the `access_token` cookie from the greytHR ESS portal, extracts `profile_id` from performance entries, and sends both to Cresto via `/greythr/connect` — no manual DevTools cookie extraction needed.
- **Kite**: content script on `mcp.kite.trade/callback` detects `status=success` and redirects back to `/portfolio` — closes the dead-end in Kite's auth flow (no callback listener).
- **Detection**: content script on `localhost` injects a DOM marker so the `/greythr` page can detect whether the extension is installed (1-second timeout, then shows install instructions).

### Packaging

- `make package-extension` — packages both Firefox (XPI via AMO signing) and Chrome (ZIP). Run after any `extension/` change.
- Firefox needs AMO credentials in `.env` (`AMO_KEY`, `AMO_SECRET`). Chrome needs nothing.
- Routes: `/greythr/extension.xpi` (Firefox), `/greythr/extension.zip` (Chrome).
- Rebuild the binary after packaging — the artifacts are embedded, not served from disk.
