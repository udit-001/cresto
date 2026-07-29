-- +goose Up
CREATE TABLE IF NOT EXISTS canonicals (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL UNIQUE,
    category        TEXT NOT NULL CHECK (category IN ('earning', 'deduction')),
    is_user_created INTEGER NOT NULL DEFAULT 0 CHECK (is_user_created IN (0, 1)),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE TABLE IF NOT EXISTS payslips (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    employer_name       TEXT NOT NULL,
    pay_period_month    INTEGER NOT NULL CHECK (pay_period_month BETWEEN 0 AND 12),
    pay_period_year     INTEGER NOT NULL,
    employee_id         TEXT NOT NULL DEFAULT '',
    designation         TEXT NOT NULL DEFAULT '',
    pay_days            INTEGER NOT NULL DEFAULT 0,
    total_days          INTEGER NOT NULL DEFAULT 0,
    gross_salary        REAL NOT NULL DEFAULT 0,
    total_deductions    REAL NOT NULL DEFAULT 0,
    net_pay             REAL NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'pending_review'
                            CHECK (status IN ('processing', 'pending_review', 'confirmed', 'failed')),
    raw_pdf_path        TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    confirmed_at        TEXT,
    batch_id            TEXT NOT NULL DEFAULT '',
    error_message       TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS payslip_components (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    payslip_id    INTEGER NOT NULL REFERENCES payslips(id) ON DELETE CASCADE,
    canonical_id  INTEGER NOT NULL REFERENCES canonicals(id),
    raw_label     TEXT NOT NULL,
    amount        REAL NOT NULL DEFAULT 0,
    ytd_amount    REAL NOT NULL DEFAULT 0,
    category      TEXT NOT NULL CHECK (category IN ('earning', 'deduction'))
);
CREATE TABLE IF NOT EXISTS upload_batches (
    id              TEXT PRIMARY KEY,
    total           INTEGER NOT NULL,
    processed_count INTEGER NOT NULL DEFAULT 0,
    failed_count    INTEGER NOT NULL DEFAULT 0,
    current_file    TEXT NOT NULL DEFAULT '',
    current_stage   TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_payslips_status    ON payslips(status);
CREATE INDEX IF NOT EXISTS idx_payslips_employer  ON payslips(employer_name);
CREATE INDEX IF NOT EXISTS idx_payslips_period    ON payslips(pay_period_year, pay_period_month);
CREATE INDEX IF NOT EXISTS idx_payslips_batch     ON payslips(batch_id);
CREATE INDEX IF NOT EXISTS idx_components_payslip ON payslip_components(payslip_id);
CREATE INDEX IF NOT EXISTS idx_components_canon   ON payslip_components(canonical_id);

-- +goose Down
DROP INDEX IF EXISTS idx_components_canon;
DROP INDEX IF EXISTS idx_components_payslip;
DROP INDEX IF EXISTS idx_payslips_batch;
DROP INDEX IF EXISTS idx_payslips_period;
DROP INDEX IF EXISTS idx_payslips_employer;
DROP INDEX IF EXISTS idx_payslips_status;
DROP TABLE IF EXISTS upload_batches;
DROP TABLE IF EXISTS payslip_components;
DROP TABLE IF EXISTS payslips;
DROP TABLE IF EXISTS canonicals;
