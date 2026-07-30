-- +goose Up
CREATE TABLE IF NOT EXISTS ais_imports (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    fy_start_year   INTEGER NOT NULL UNIQUE,
    raw_json_path   TEXT NOT NULL DEFAULT '',
    imported_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- +goose Down
DROP TABLE IF EXISTS ais_imports;
