# Cresto

Your income grows, your wealth grows, your financial understanding grows.

Server-rendered Go monolith for tracking income from payslip PDFs. Uses a local LLM (LM Studio) for automated vision-based extraction.

## Quick start

```sh
make dev
```

Opens at `http://localhost:7777`. Point it at your payslip PDFs and Cresto extracts, categorises, and charts your income over time.

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
