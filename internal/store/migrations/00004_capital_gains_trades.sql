-- +goose Up
CREATE TABLE IF NOT EXISTS capital_gains_trades (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    fy_start_year   INTEGER NOT NULL,
    section         TEXT NOT NULL DEFAULT '',
    symbol          TEXT NOT NULL DEFAULT '',
    isin            TEXT NOT NULL DEFAULT '',
    entry_date      TEXT NOT NULL DEFAULT '',
    exit_date       TEXT NOT NULL DEFAULT '',
    quantity        REAL NOT NULL DEFAULT 0,
    buy_value       REAL NOT NULL DEFAULT 0,
    sell_value      REAL NOT NULL DEFAULT 0,
    profit          REAL NOT NULL DEFAULT 0,
    taxable_profit  REAL NOT NULL DEFAULT 0,
    fmv             REAL NOT NULL DEFAULT 0,
    stt             REAL NOT NULL DEFAULT 0,
    imported_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_capital_gains_fy ON capital_gains_trades(fy_start_year);

-- +goose Down
DROP TABLE IF EXISTS capital_gains_trades;
