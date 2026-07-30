-- +goose Up
CREATE TABLE IF NOT EXISTS taxpayer_profile (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    pan                 TEXT NOT NULL DEFAULT '',
    dob                 TEXT NOT NULL DEFAULT '',
    declarant_name      TEXT NOT NULL DEFAULT '',
    verification_place  TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE IF NOT EXISTS bank_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ifsc            TEXT NOT NULL DEFAULT '',
    account_number  TEXT NOT NULL DEFAULT '',
    account_type    TEXT NOT NULL DEFAULT 'savings' CHECK (account_type IN ('savings', 'current')),
    bank_name       TEXT NOT NULL DEFAULT '',
    is_primary      INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- +goose Down
DROP TABLE IF EXISTS bank_accounts;
DROP TABLE IF EXISTS taxpayer_profile;
