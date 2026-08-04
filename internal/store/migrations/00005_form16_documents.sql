-- +goose Up
CREATE TABLE IF NOT EXISTS form16_documents (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    employer_name   TEXT NOT NULL,
    fy_start_year   INTEGER NOT NULL,
    part            TEXT NOT NULL CHECK (part IN ('A', 'B')),
    source          TEXT NOT NULL DEFAULT 'manual',
    file_path       TEXT NOT NULL,
    fetched_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE(employer_name, fy_start_year, part)
);

-- +goose Down
DROP TABLE IF EXISTS form16_documents;
