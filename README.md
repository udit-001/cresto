# Cresto

Your income grows, your wealth grows, your financial understanding grows.

Server-rendered Go monolith for tracking income from payslips and broker holdings. Uses a local LLM (LM Studio) for automated vision-based PDF extraction, with direct JSON auto-fetch from greytHR.

## Quick start

```sh
make dev
```

Opens at `http://localhost:7777`. Point it at your payslip PDFs and Cresto extracts, categorises, and charts your income over time.

## Features

- **Payslip ingestion** — PDF upload with LLM vision extraction, or auto-fetch from greytHR (JSON-first, no LLM needed)
- **Broker holdings** — live equity + MF holdings from Groww and Zerodha (Kite) via MCP
- **Browser extension** — one-click greytHR connection and Kite auth redirect (Firefox + Chrome)
- **Charts & analytics** — YTD breakdowns, year-over-year trends, canonical component aggregation

## Stack

**Go 1.26.5** + **SQLite** (pure Go, no CGO) + **html/template** + **Tailwind CSS v4** + **Basecoat** + **Chart.js**

All data stays on your machine at `~/.cresto/`.

## Commands

| `make` target | What it does |
|---|---|
| `build` | Rebuild CSS + compile → `bin/cresto` |
| `dev` | Rebuild CSS + start `--foreground` |
| `start`/`stop` | Daemon mode |
| `test` | `go test ./...` |
| `migrate-up` | Run pending migrations (auto-runs on start) |
| `package-extension` | Package browser extension for Firefox + Chrome |
