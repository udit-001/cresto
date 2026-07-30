// Package store is the SQLite persistence layer for parsed payslips.
// It owns versioned schema migrations (goose), canonical-component seeding,
// and all queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Category partitions components into earnings or deductions.
type Category string

const (
	CategoryEarning   Category = "earning"
	CategoryDeduction Category = "deduction"
)

// Status marks where a payslip sits in the review flow.
type Status string

const (
	StatusProcessing    Status = "processing"
	StatusPendingReview Status = "pending_review"
	StatusConfirmed     Status = "confirmed"
	StatusFailed        Status = "failed"
)

// Canonical is a stable, cross-payslip component name. The 21 seed rows
// cover the common Indian payroll vocabulary; users extend it during review.
type Canonical struct {
	ID            int64
	Name          string
	Category      Category
	IsUserCreated bool
	CreatedAt     string
}

// seedDisplayNames maps slug-like seed names to human-readable labels for the
// UI. User-created canonicals use whatever name the user typed (their Name is
// already a display label, not a slug), so they aren't listed here.
var seedDisplayNames = map[string]string{
	"basic":             "Basic",
	"hra":               "HRA",
	"da":                "Dearness Allowance",
	"conveyance":        "Conveyance",
	"medical":           "Medical",
	"lta":               "LTA",
	"education":         "Education",
	"telephone":         "Telephone",
	"special_allowance": "Special Allowance",
	"bonus":             "Bonus",
	"arrears":           "Arrears",
	"leave_encashment":  "Leave Encashment",
	"other_earnings":          "Other Earnings",
	"term_insurance_earning":  "Term Insurance",
	"medical_insurance_earning": "Medical Insurance",
	"epf":                     "EPF",
	"professional_tax":  "Professional Tax",
	"tds":               "TDS",
	"esi":               "ESI",
	"lwf":               "LWF",
	"lop":                     "LOP",
	"loan_recovery":           "Loan Recovery",
	"term_insurance_deduction": "Term Insurance",
	"medical_insurance_deduction": "Medical Insurance",
	"other_deductions":       "Other Deductions",
}

// DisplayName returns the human-readable label for this canonical. Seed
// canonicals have explicit display names (e.g. "basic" → "Basic", "epf" →
// "EPF"); user-created canonicals return their Name as-is since the user
// already typed a human-readable label. Unknown slugs fall back to
// title-casing with underscores replaced by spaces.
func (c Canonical) DisplayName() string {
	if c.IsUserCreated {
		return c.Name
	}
	if d, ok := seedDisplayNames[c.Name]; ok {
		return d
	}
	return titleCaseSlug(c.Name)
}

func titleCaseSlug(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// Component is one line item on a payslip. RawLabel preserves the original
// payslip text; CanonicalID ties it to the stable vocabulary.
type Component struct {
	ID          int64
	PayslipID   int64
	CanonicalID int64
	RawLabel    string
	Amount      float64
	YTDAmt      float64
	Category    Category
}

// Payslip is one parsed period. Components is filled by GetPayslip and empty
// for list/timeline queries (use GetComponentTimeline for series data).
type Payslip struct {
	ID              int64
	EmployerName    string
	PayPeriodMonth  int
	PayPeriodYear   int
	EmployeeID      string
	Designation     string
	PayDays         int
	TotalDays       int
	GrossSalary     float64
	TotalDeductions float64
	NetPay          float64
	Status          Status
	RawPDFPath      string
	CreatedAt       string
	ConfirmedAt     string
	BatchID         string
	ErrorMessage    string
	Components      []Component
}

// Filter narrows ListPayslips. Zero values mean "no constraint on this field".
type Filter struct {
	Status    Status
	Employer  string
	YearFrom  int
	YearTo    int
	MonthFrom int
	MonthTo   int
	BatchID   string
	Limit     int
	Offset    int
}

// Batch is one bulk upload's progress record. The background processor
// increments ProcessedCount/FailedCount as each PDF is handled.
type Batch struct {
	ID             string
	Total          int
	ProcessedCount int
	FailedCount    int
	CurrentFile    string
	CurrentStage   string
	CreatedAt      string
}

// ComponentPoint is one month's value of a single canonical component.
type ComponentPoint struct {
	PayslipID      int64
	PayPeriodYear  int
	PayPeriodMonth int
	Amount         float64
	YTDAmt         float64
	RawLabel       string
}

// Store wraps a *sql.DB. Open one per app lifetime; share across goroves
// (database/sql serializes access to the underlying connection pool).
type Store struct {
	db *sql.DB
}

// seedCanonicals is the first-run vocabulary from PF-1: 13 earnings + 8 deductions.
// Ordering is stable so seed IDs are deterministic across fresh databases.
var seedCanonicals = []struct {
	Name     string
	Category Category
}{
	{"basic", CategoryEarning},
	{"hra", CategoryEarning},
	{"da", CategoryEarning},
	{"conveyance", CategoryEarning},
	{"medical", CategoryEarning},
	{"lta", CategoryEarning},
	{"education", CategoryEarning},
	{"telephone", CategoryEarning},
	{"special_allowance", CategoryEarning},
	{"bonus", CategoryEarning},
	{"arrears", CategoryEarning},
	{"leave_encashment", CategoryEarning},
	{"term_insurance_earning", CategoryEarning},
	{"medical_insurance_earning", CategoryEarning},
	{"other_earnings", CategoryEarning},
	{"epf", CategoryDeduction},
	{"professional_tax", CategoryDeduction},
	{"tds", CategoryDeduction},
	{"esi", CategoryDeduction},
	{"lwf", CategoryDeduction},
	{"lop", CategoryDeduction},
	{"loan_recovery", CategoryDeduction},
	{"term_insurance_deduction", CategoryDeduction},
	{"medical_insurance_deduction", CategoryDeduction},
	{"other_deductions", CategoryDeduction},
}

// ErrNotFound is returned when a single-row lookup (GetPayslip, GetComponentTimeline
// against an unknown canonical, etc.) matches no rows.
var ErrNotFound = errors.New("store: not found")

// Open creates or opens the SQLite database at path, runs migrations, and seeds
// canonicals if the table is empty. The caller owns closing via Close.
func Open(path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Legacy migration for databases created before goose was introduced.
	// On fresh DBs this is a no-op (the payslips table doesn't exist yet).
	if err := migrateLegacy(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("legacy migrate: %w", err)
	}

	provider, err := gooseProvider(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := provider.Up(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("goose up: %w", err)
	}
	if err := seedCanonicalVocab(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("seed canonicals: %w", err)
	}
	return &Store{db: db}, nil
}

// MigrateAction runs a manual goose operation on the database at path.
// action is one of: "up", "down", "status". It opens a standalone connection
// (separate from Open) so the server can keep running if needed.
func MigrateAction(path, action string) error {
	db, err := openDB(path)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider, err := gooseProvider(db)
	if err != nil {
		return err
	}

	switch action {
	case "up":
		res, err := provider.Up(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "applied %d migration(s)\n", len(res))
	case "down":
		res, err := provider.Down(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "rolled back: version %d (%s)\n", res.Source.Version, res.Source.Path)
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			state := "pending"
			if s.State == goose.StateApplied {
				state = "applied"
			}
			fmt.Fprintf(os.Stderr, "  %4d  %-7s  %s\n", s.Source.Version, state, s.Source.Path)
		}
	default:
		return fmt.Errorf("unknown migrate action %q (want up, down, or status)", action)
	}
	return nil
}

// openDB creates the SQLite connection with pragmas (busy timeout, foreign
// keys, WAL) but does NOT run migrations. Shared by Open and MigrateAction.
func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// SQLite serializes writers; one connection lets database/sql avoid
	// "database is locked" surprises on concurrent writes from one process.
	db.SetMaxOpenConns(1)
	return db, nil
}

// migrateLegacy upgrades databases created before goose was introduced. These
// lack batch_id, error_message, and the expanded status CHECK. Fresh DBs or
// databases already managed by goose are no-ops — it checks for the batch_id
// column and skips if present. Idempotent.
func migrateLegacy(ctx context.Context, db *sql.DB) error {
	var tableExists int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='payslips'`).Scan(&tableExists); err != nil {
		return err
	}
	if tableExists == 0 {
		return nil
	}
	if hasColumn(ctx, db, "payslips", "batch_id") {
		return nil
	}

	// Rebuild payslips with the expanded status CHECK + relaxed month + new columns.
	stmts := []string{
		`CREATE TABLE payslips_new (
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
		)`,
		`INSERT INTO payslips_new (
			id, employer_name, pay_period_month, pay_period_year,
			employee_id, designation, pay_days, total_days,
			gross_salary, total_deductions, net_pay,
			status, raw_pdf_path, created_at, confirmed_at
		) SELECT
			id, employer_name, pay_period_month, pay_period_year,
			employee_id, designation, pay_days, total_days,
			gross_salary, total_deductions, net_pay,
			status, raw_pdf_path, created_at, confirmed_at
		FROM payslips`,
		`DROP TABLE payslips`,
		`ALTER TABLE payslips_new RENAME TO payslips`,
		`CREATE INDEX IF NOT EXISTS idx_payslips_status   ON payslips(status)`,
		`CREATE INDEX IF NOT EXISTS idx_payslips_employer ON payslips(employer_name)`,
		`CREATE INDEX IF NOT EXISTS idx_payslips_period   ON payslips(pay_period_year, pay_period_month)`,
		`CREATE INDEX IF NOT EXISTS idx_payslips_batch    ON payslips(batch_id)`,
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate step %q: %w", firstLine(s), err)
		}
	}
	return tx.Commit()
}

// hasColumn reports whether table has a column named col.
func hasColumn(ctx context.Context, db *sql.DB, table, col string) bool {
	var n int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`,
		table, col).Scan(&n)
	return n > 0
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// gooseProvider returns a goose provider over the embedded migrations.
func gooseProvider(db *sql.DB) (*goose.Provider, error) {
	migFS, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("sub migrations fs: %w", err)
	}
	return goose.NewProvider(goose.DialectSQLite3, db, migFS)
}

// seedCanonicalVocab inserts the seed vocabulary using INSERT OR IGNORE, so
// it's idempotent: existing canonicals are left untouched, new ones are
// added. This handles both fresh databases (all inserted) and existing
// databases that predate a seed-list change (only new canonicals are
// inserted). User-created canonicals (is_user_created = 1) are never
// affected.
func seedCanonicalVocab(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const q = `INSERT OR IGNORE INTO canonicals (name, category, is_user_created) VALUES (?, ?, 0)`
	for _, c := range seedCanonicals {
		if _, err := tx.ExecContext(ctx, q, c.Name, string(c.Category)); err != nil {
			return fmt.Errorf("seed %s: %w", c.Name, err)
		}
	}
	return tx.Commit()
}

// DB returns the underlying database handle for ad-hoc queries.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

// ListCanonicals returns every canonical component, ordered by category then name.
func (s *Store) ListCanonicals(ctx context.Context) ([]Canonical, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, category, is_user_created, created_at
		FROM canonicals
		ORDER BY category, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Canonical
	for rows.Next() {
		var c Canonical
		var cat string
		var uc int
		if err := rows.Scan(&c.ID, &c.Name, &cat, &uc, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Category = Category(cat)
		c.IsUserCreated = uc == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// FindCanonicalByName returns the canonical with the given name (e.g. "basic").
// Used by simple label-mappers that map raw payslip text to the vocabulary.
func (s *Store) FindCanonicalByName(ctx context.Context, name string) (Canonical, error) {
	var c Canonical
	var cat string
	var uc int
	q := `SELECT id, name, category, is_user_created, created_at FROM canonicals WHERE name = ?`
	err := s.db.QueryRowContext(ctx, q, name).Scan(&c.ID, &c.Name, &cat, &uc, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Canonical{}, ErrNotFound
	}
	if err != nil {
		return Canonical{}, err
	}
	c.Category = Category(cat)
	c.IsUserCreated = uc == 1
	return c, nil
}

// CreateCanonical adds a user-created canonical and returns it.
func (s *Store) CreateCanonical(ctx context.Context, name string, cat Category) (Canonical, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO canonicals (name, category, is_user_created) VALUES (?, ?, 1)`,
		name, string(cat))
	if err != nil {
		return Canonical{}, err
	}
	id, _ := res.LastInsertId()
	return Canonical{ID: id, Name: name, Category: cat, IsUserCreated: true}, nil
}

// SavePayslip inserts a payslip and its components atomically. Payslip.ID is
// set on success; each component's ID and PayslipID are filled in too.
// Status defaults to pending_review; CreatedAt is set by the database.
func (s *Store) SavePayslip(ctx context.Context, p *Payslip) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const insertPayslip = `
		INSERT INTO payslips (
			employer_name, pay_period_month, pay_period_year,
			employee_id, designation, pay_days, total_days,
			gross_salary, total_deductions, net_pay,
			status, raw_pdf_path, batch_id, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	status := string(p.Status)
	if status == "" {
		status = string(StatusPendingReview)
	}
	res, err := tx.ExecContext(ctx, insertPayslip,
		p.EmployerName, p.PayPeriodMonth, p.PayPeriodYear,
		p.EmployeeID, p.Designation, p.PayDays, p.TotalDays,
		p.GrossSalary, p.TotalDeductions, p.NetPay,
		status, p.RawPDFPath, p.BatchID, p.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("insert payslip: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = id
	p.Status = Status(status)

	const insertComp = `
		INSERT INTO payslip_components (payslip_id, canonical_id, raw_label, amount, ytd_amount, category)
		VALUES (?, ?, ?, ?, ?, ?)`
	for i := range p.Components {
		c := &p.Components[i]
		c.PayslipID = id
		res, err := tx.ExecContext(ctx, insertComp,
			id, c.CanonicalID, c.RawLabel, c.Amount, c.YTDAmt, string(c.Category))
		if err != nil {
			return fmt.Errorf("insert component %q: %w", c.RawLabel, err)
		}
		c.ID, _ = res.LastInsertId()
	}
	return tx.Commit()
}

// GetPayslip fetches a single payslip with its components, ordered by category then raw_label.
func (s *Store) GetPayslip(ctx context.Context, id int64) (Payslip, error) {
	var p Payslip
	var status string
	payQ := `
		SELECT id, employer_name, pay_period_month, pay_period_year,
		       employee_id, designation, pay_days, total_days,
		       gross_salary, total_deductions, net_pay,
		       status, raw_pdf_path, created_at, COALESCE(confirmed_at, ''),
		       batch_id, error_message
		FROM payslips WHERE id = ?`
	err := s.db.QueryRowContext(ctx, payQ, id).Scan(
		&p.ID, &p.EmployerName, &p.PayPeriodMonth, &p.PayPeriodYear,
		&p.EmployeeID, &p.Designation, &p.PayDays, &p.TotalDays,
		&p.GrossSalary, &p.TotalDeductions, &p.NetPay,
		&status, &p.RawPDFPath, &p.CreatedAt, &p.ConfirmedAt,
		&p.BatchID, &p.ErrorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Payslip{}, ErrNotFound
	}
	if err != nil {
		return Payslip{}, err
	}
	p.Status = Status(status)

	// Earnings before deductions: matches the human-readable payslip layout.
	comps, err := s.scanComponents(ctx, `SELECT id, payslip_id, canonical_id, raw_label, amount, ytd_amount, category
		FROM payslip_components WHERE payslip_id = ?
		ORDER BY CASE category WHEN 'earning' THEN 0 ELSE 1 END, raw_label`, id)
	if err != nil {
		return Payslip{}, err
	}
	p.Components = comps
	return p, nil
}

func (s *Store) scanComponents(ctx context.Context, query string, args ...any) ([]Component, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Component
	for rows.Next() {
		var c Component
		var cat string
		if err := rows.Scan(&c.ID, &c.PayslipID, &c.CanonicalID, &c.RawLabel, &c.Amount, &c.YTDAmt, &cat); err != nil {
			return nil, err
		}
		c.Category = Category(cat)
		out = append(out, c)
	}
	return out, rows.Err()
}

// filterWhereClause builds the WHERE clause and args for a Filter. Shared by
// ListPayslips and CountPayslips so the filter logic has one source of truth.
//
// When both year and month are set for a bound, uses YYYYMM comparison
// (pay_period_year * 100 + pay_period_month) so cross-year ranges like an
// Indian financial year (April–March) work as a continuous range. Year-only
// or month-only bounds fall back to independent comparisons.
func filterWhereClause(f Filter) (string, []any) {
	var clause []string
	var args []any
	if f.Status != "" {
		clause = append(clause, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Employer != "" {
		clause = append(clause, "employer_name = ?")
		args = append(args, f.Employer)
	}
	if f.YearFrom != 0 && f.MonthFrom != 0 {
		clause = append(clause, "(pay_period_year * 100 + pay_period_month) >= ?")
		args = append(args, f.YearFrom*100+f.MonthFrom)
	} else if f.YearFrom != 0 {
		clause = append(clause, "pay_period_year >= ?")
		args = append(args, f.YearFrom)
	} else if f.MonthFrom != 0 {
		clause = append(clause, "pay_period_month >= ?")
		args = append(args, f.MonthFrom)
	}
	if f.YearTo != 0 && f.MonthTo != 0 {
		clause = append(clause, "(pay_period_year * 100 + pay_period_month) <= ?")
		args = append(args, f.YearTo*100+f.MonthTo)
	} else if f.YearTo != 0 {
		clause = append(clause, "pay_period_year <= ?")
		args = append(args, f.YearTo)
	} else if f.MonthTo != 0 {
		clause = append(clause, "pay_period_month <= ?")
		args = append(args, f.MonthTo)
	}
	if f.BatchID != "" {
		clause = append(clause, "batch_id = ?")
		args = append(args, f.BatchID)
	}
	if len(clause) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clause, " AND "), args
}

// ListPayslips returns payslips matching the filter (without components),
// newest-period first. Empty filter returns all payslips.
func (s *Store) ListPayslips(ctx context.Context, f Filter) ([]Payslip, error) {
	where, args := filterWhereClause(f)
	q := `SELECT id, employer_name, pay_period_month, pay_period_year,
	             employee_id, designation, pay_days, total_days,
	             gross_salary, total_deductions, net_pay,
	             status, raw_pdf_path, created_at, COALESCE(confirmed_at, ''),
	             batch_id, error_message
	      FROM payslips` + where + `
	      ORDER BY pay_period_year DESC, pay_period_month DESC, id DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
		if f.Offset > 0 {
			q += fmt.Sprintf(" OFFSET %d", f.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payslip
	for rows.Next() {
		var p Payslip
		var status string
		if err := rows.Scan(
			&p.ID, &p.EmployerName, &p.PayPeriodMonth, &p.PayPeriodYear,
			&p.EmployeeID, &p.Designation, &p.PayDays, &p.TotalDays,
			&p.GrossSalary, &p.TotalDeductions, &p.NetPay,
			&status, &p.RawPDFPath, &p.CreatedAt, &p.ConfirmedAt,
			&p.BatchID, &p.ErrorMessage,
		); err != nil {
			return nil, err
		}
		p.Status = Status(status)
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountPayslips returns the number of payslips matching the filter.
// Uses the same WHERE clause as ListPayslips but with COUNT(*) instead.
func (s *Store) CountPayslips(ctx context.Context, f Filter) (int, error) {
	where, args := filterWhereClause(f)
	q := "SELECT COUNT(*) FROM payslips" + where
	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListPendingReview is ListPayslips with Status=pending_review.
func (s *Store) ListPendingReview(ctx context.Context) ([]Payslip, error) {
	return s.ListPayslips(ctx, Filter{Status: StatusPendingReview})
}

// ConfirmPayslip flips status to confirmed and stamps confirmed_at.
// Returns ErrNotFound if the payslip does not exist.
func (s *Store) ConfirmPayslip(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payslips
		   SET status = 'confirmed', confirmed_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
		       error_message = ''
		 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ConfirmPayslipsByStatus confirms every payslip with the given status.
// Returns the count of payslips confirmed. Empty status is rejected.
func (s *Store) ConfirmPayslipsByStatus(ctx context.Context, status Status) (int, error) {
	if status == "" {
		return 0, errors.New("store: ConfirmPayslipsByStatus requires non-empty status")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE payslips
		   SET status = 'confirmed', confirmed_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
		       error_message = ''
		 WHERE status = ?`, string(status))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ConfirmPayslipsByIDs confirms the payslips with the given IDs. Non-pending
// payslips are unaffected (the UPDATE sets status='confirmed' regardless, but
// callers should only pass pending_review IDs for meaningful results). Returns
// the count of payslips confirmed. Empty IDs is rejected.
func (s *Store) ConfirmPayslipsByIDs(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, errors.New("store: ConfirmPayslipsByIDs requires at least one ID")
	}
	placeholders := strings.Repeat("?,", len(ids)-1) + "?"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE payslips
		   SET status = 'confirmed', confirmed_at = strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now'),
		       error_message = ''
		 WHERE id IN (%s)`, placeholders), args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeletePayslip hard-deletes a payslip row. Components are removed by the
// schema's ON DELETE CASCADE constraint. Does NOT touch the PDF file on disk —
// file lifecycle is the handler's job, the store owns data only. Returns
// ErrNotFound if the payslip does not exist.
func (s *Store) DeletePayslip(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM payslips WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePayslipsByStatus hard-deletes every payslip with the given status.
// Components are removed by the schema's ON DELETE CASCADE constraint.
// Returns the deleted payslips (with RawPDFPath populated) so the caller
// can clean up PDF files on disk — the store owns data only, not files.
// Empty status is rejected (won't delete everything by accident).
func (s *Store) DeletePayslipsByStatus(ctx context.Context, status Status) ([]Payslip, error) {
	if status == "" {
		return nil, errors.New("store: DeletePayslipsByStatus requires non-empty status")
	}
	// Read first so we can return what was deleted for PDF cleanup.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, raw_pdf_path FROM payslips WHERE status = ?`, string(status))
	if err != nil {
		return nil, err
	}
	var deleted []Payslip
	for rows.Next() {
		var p Payslip
		if err := rows.Scan(&p.ID, &p.RawPDFPath); err != nil {
			rows.Close()
			return nil, err
		}
		deleted = append(deleted, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM payslips WHERE status = ?`, string(status)); err != nil {
		return nil, err
	}
	return deleted, nil
}

// DeletePayslipsByEmployer hard-deletes every payslip with the given employer
// name. Components are removed by the schema's ON DELETE CASCADE constraint.
// Returns the deleted payslips (with RawPDFPath populated) so the caller
// can clean up PDF files on disk — the store owns data only, not files.
// Empty employer is rejected (won't delete everything by accident).
func (s *Store) DeletePayslipsByEmployer(ctx context.Context, employer string) ([]Payslip, error) {
	if employer == "" {
		return nil, errors.New("store: DeletePayslipsByEmployer requires non-empty employer")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, raw_pdf_path FROM payslips WHERE employer_name = ?`, employer)
	if err != nil {
		return nil, err
	}
	var deleted []Payslip
	for rows.Next() {
		var p Payslip
		if err := rows.Scan(&p.ID, &p.RawPDFPath); err != nil {
			rows.Close()
			return nil, err
		}
		deleted = append(deleted, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM payslips WHERE employer_name = ?`, employer); err != nil {
		return nil, err
	}
	return deleted, nil
}

// DeletePayslipsByIDs hard-deletes the payslips with the given IDs. Components
// are removed by ON DELETE CASCADE. Returns the deleted payslips (with
// RawPDFPath populated) so the caller can clean up PDF files on disk.
// Empty IDs is rejected.
func (s *Store) DeletePayslipsByIDs(ctx context.Context, ids []int64) ([]Payslip, error) {
	if len(ids) == 0 {
		return nil, errors.New("store: DeletePayslipsByIDs requires at least one ID")
	}
	placeholders := strings.Repeat("?,", len(ids)-1) + "?"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf("SELECT id, raw_pdf_path FROM payslips WHERE id IN (%s)", placeholders), args...)
	if err != nil {
		return nil, err
	}
	var deleted []Payslip
	for rows.Next() {
		var p Payslip
		if err := rows.Scan(&p.ID, &p.RawPDFPath); err != nil {
			rows.Close()
			return nil, err
		}
		deleted = append(deleted, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM payslips WHERE id IN (%s)", placeholders), args...); err != nil {
		return nil, err
	}
	return deleted, nil
}

// ListPendingReviewChronological returns pending payslips oldest-period-first
// so the review queue walks the user through time rather than by insert order.
// Payslips with an unparsed period (month/year = 0) sort last by period but
// retain ID ordering within that group so they're still deterministic.
func (s *Store) ListPendingReviewChronological(ctx context.Context) ([]Payslip, error) {
	// NULLIF/COALESCE trick: a zero period sorts after real ones when we
	// treat 0 as a large number for ordering purposes only.
	q := `SELECT id, employer_name, pay_period_month, pay_period_year,
	             employee_id, designation, pay_days, total_days,
	             gross_salary, total_deductions, net_pay,
	             status, raw_pdf_path, created_at, COALESCE(confirmed_at, ''),
	             batch_id, error_message
	      FROM payslips
	      WHERE status = 'pending_review'
	      ORDER BY CASE WHEN pay_period_year = 0 THEN 9999 ELSE pay_period_year END ASC,
	               CASE WHEN pay_period_month = 0 THEN 13 ELSE pay_period_month END ASC,
	               id ASC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payslip
	for rows.Next() {
		var p Payslip
		var status string
		if err := rows.Scan(
			&p.ID, &p.EmployerName, &p.PayPeriodMonth, &p.PayPeriodYear,
			&p.EmployeeID, &p.Designation, &p.PayDays, &p.TotalDays,
			&p.GrossSalary, &p.TotalDeductions, &p.NetPay,
			&status, &p.RawPDFPath, &p.CreatedAt, &p.ConfirmedAt,
			&p.BatchID, &p.ErrorMessage,
		); err != nil {
			return nil, err
		}
		p.Status = Status(status)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListFailed returns payslips in the failed status, newest-first. Shown in the
// review queue's failed section with per-row retry buttons.
func (s *Store) ListFailed(ctx context.Context) ([]Payslip, error) {
	return s.ListPayslips(ctx, Filter{Status: StatusFailed})
}

// ListYears returns the distinct pay period years across all payslips,
// ordered descending. Used to populate the year filter dropdown.
func (s *Store) ListYears(ctx context.Context) ([]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT pay_period_year FROM payslips
		 WHERE pay_period_year != 0
		 ORDER BY pay_period_year DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

// ListEmployers returns the distinct employer names across all payslips,
// ordered alphabetically. Used to populate the company filter dropdown.
func (s *Store) ListEmployers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT employer_name FROM payslips
		WHERE employer_name != ''
		ORDER BY employer_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// MarkPayslipProcessing flips a payslip to processing and clears any prior
// error_message. Used when retrying a failed parse so the UI can show the
// retry is underway.
func (s *Store) MarkPayslipProcessing(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payslips SET status = 'processing', error_message = ''
		WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkPayslipFailed flips a payslip to failed and records the error message.
// Does not touch batch counters — those reflect the original batch run, not retries.
func (s *Store) MarkPayslipFailed(ctx context.Context, id int64, errMsg string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payslips SET status = 'failed', error_message = ?
		WHERE id = ?`, errMsg, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateBatch inserts a new upload batch row. total is the number of PDFs in
// the batch; processed/failed counts start at zero and are incremented by the
// background processor as each PDF is handled.
func (s *Store) CreateBatch(ctx context.Context, id string, total int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO upload_batches (id, total) VALUES (?, ?)`, id, total)
	return err
}

func (s *Store) UpdateBatchProgress(ctx context.Context, batchID, currentFile, currentStage string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE upload_batches SET current_file = ?, current_stage = ? WHERE id = ?`,
		currentFile, currentStage, batchID)
	return err
}

// GetBatch returns the batch's current progress, or ErrNotFound if no such batch.
// GetActiveBatchID returns the ID of the most recent incomplete batch
// (processed_count + failed_count < total), or empty string if none exists.
func (s *Store) GetActiveBatchID(ctx context.Context) string {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM upload_batches
		 WHERE processed_count + failed_count < total
		 ORDER BY created_at DESC LIMIT 1`).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

func (s *Store) GetBatch(ctx context.Context, id string) (Batch, error) {
	var b Batch
	err := s.db.QueryRowContext(ctx,
		`SELECT id, total, processed_count, failed_count, current_file, current_stage, created_at
		 FROM upload_batches WHERE id = ?`, id).
		Scan(&b.ID, &b.Total, &b.ProcessedCount, &b.FailedCount, &b.CurrentFile, &b.CurrentStage, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	return b, err
}

// IncrementBatchProcessed atomically bumps processed_count for a batch.
// Called once per successfully-parsed PDF.
func (s *Store) IncrementBatchProcessed(ctx context.Context, batchID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE upload_batches SET processed_count = processed_count + 1 WHERE id = ?`, batchID)
	return err
}

// IncrementBatchFailed atomically bumps failed_count for a batch.
// Called once per failed PDF so the progress page can show "X of Y done".
func (s *Store) IncrementBatchFailed(ctx context.Context, batchID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE upload_batches SET failed_count = failed_count + 1 WHERE id = ?`, batchID)
	return err
}

// UpdatePayslip applies edits to an existing payslip in one transaction:
// payslip row is updated (including status, clearing any prior error_message),
// components are fully replaced (delete + re-insert). Payslip.ID must be set.
// Set p.Status to the desired status before calling: the review save flow
// keeps the existing status; the batch/retry success path sets it to
// pending_review. Components[].ID is filled for newly inserted rows.
func (s *Store) UpdatePayslip(ctx context.Context, p *Payslip) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	status := string(p.Status)
	if status == "" {
		status = string(StatusPendingReview)
	}
	const updateP = `
		UPDATE payslips SET
			employer_name = ?, pay_period_month = ?, pay_period_year = ?,
			employee_id = ?, designation = ?, pay_days = ?, total_days = ?,
			gross_salary = ?, total_deductions = ?, net_pay = ?,
			status = ?, error_message = ''
		WHERE id = ?`
	res, err := tx.ExecContext(ctx, updateP,
		p.EmployerName, p.PayPeriodMonth, p.PayPeriodYear,
		p.EmployeeID, p.Designation, p.PayDays, p.TotalDays,
		p.GrossSalary, p.TotalDeductions, p.NetPay,
		status, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update payslip: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM payslip_components WHERE payslip_id = ?`, p.ID); err != nil {
		return fmt.Errorf("delete old components: %w", err)
	}

	const insertComp = `
		INSERT INTO payslip_components (payslip_id, canonical_id, raw_label, amount, ytd_amount, category)
		VALUES (?, ?, ?, ?, ?, ?)`
	for i := range p.Components {
		c := &p.Components[i]
		c.PayslipID = p.ID
		c.ID = 0
		compRes, err := tx.ExecContext(ctx, insertComp,
			p.ID, c.CanonicalID, c.RawLabel, c.Amount, c.YTDAmt, string(c.Category))
		if err != nil {
			return fmt.Errorf("insert component %q: %w", c.RawLabel, err)
		}
		c.ID, _ = compRes.LastInsertId()
	}
	return tx.Commit()
}

// GetIncomeTimeline returns all confirmed payslips oldest-period-first,
// for the dashboard's headline numbers and trend chart.
// Pass includePending=true to include pending_review rows (e.g. preview).
func (s *Store) GetIncomeTimeline(ctx context.Context, includePending bool) ([]Payslip, error) {
	f := Filter{}
	if !includePending {
		f.Status = StatusConfirmed
	}
	ps, err := s.ListPayslips(ctx, f)
	if err != nil {
		return nil, err
	}
	// ListPayslips is newest-first; reverse for chronological order.
	for i, j := 0, len(ps)-1; i < j; i, j = i+1, j-1 {
		ps[i], ps[j] = ps[j], ps[i]
	}
	return ps, nil
}

// GetConfirmedTimeline returns all confirmed payslips oldest-period-first,
// each with its Components populated. The dashboard uses this single query to
// build headline numbers, deltas, YTD totals, and chart series — without an
// N+1 follow-up per payslip. Payslips with no components are still included.
func (s *Store) GetConfirmedTimeline(ctx context.Context) ([]Payslip, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.employer_name, p.pay_period_month, p.pay_period_year,
		       p.employee_id, p.designation, p.pay_days, p.total_days,
		       p.gross_salary, p.total_deductions, p.net_pay,
		       p.status, p.raw_pdf_path, p.created_at, COALESCE(p.confirmed_at, ''),
		       c.id, c.payslip_id, c.canonical_id, c.raw_label, c.amount, c.ytd_amount, c.category
		FROM payslips p
		LEFT JOIN payslip_components c ON c.payslip_id = p.id
		WHERE p.status = 'confirmed'
		ORDER BY p.pay_period_year ASC, p.pay_period_month ASC, p.id ASC,
		         CASE c.category WHEN 'earning' THEN 0 ELSE 1 END, c.raw_label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Order matches GetPayslip: chronological, then earning-before-deduction.
	// Map payslip ID → *Payslip so we can attach multiple component rows.
	byID := map[int64]*Payslip{}
	var order []int64
	for rows.Next() {
		var (
			p           Payslip
			status      string
			cID         sql.NullInt64
			cPayslipID  sql.NullInt64
			cCanonID    sql.NullInt64
			cRawLabel   sql.NullString
			cAmount     sql.NullFloat64
			cYTD        sql.NullFloat64
			cCategory   sql.NullString
		)
		if err := rows.Scan(
			&p.ID, &p.EmployerName, &p.PayPeriodMonth, &p.PayPeriodYear,
			&p.EmployeeID, &p.Designation, &p.PayDays, &p.TotalDays,
			&p.GrossSalary, &p.TotalDeductions, &p.NetPay,
			&status, &p.RawPDFPath, &p.CreatedAt, &p.ConfirmedAt,
			&cID, &cPayslipID, &cCanonID, &cRawLabel, &cAmount, &cYTD, &cCategory,
		); err != nil {
			return nil, err
		}
		p.Status = Status(status)

		ptr, ok := byID[p.ID]
		if !ok {
			ptr = &p
			byID[p.ID] = ptr
			order = append(order, p.ID)
		}
		// LEFT JOIN gives NULL for payslips with no components.
		if cID.Valid {
			ptr.Components = append(ptr.Components, Component{
				ID:          cID.Int64,
				PayslipID:   cPayslipID.Int64,
				CanonicalID: cCanonID.Int64,
				RawLabel:    cRawLabel.String,
				Amount:      cAmount.Float64,
				YTDAmt:      cYTD.Float64,
				Category:    Category(cCategory.String),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Payslip, len(order))
	for i, id := range order {
		out[i] = *byID[id]
	}
	return out, nil
}

// GetFYConfirmedTimeline returns confirmed payslips in the Indian financial
// year that starts on April 1 of fyStartYear (i.e. April fyStartYear through
// March fyStartYear+1). Each payslip has its Components populated. The slice
// is chronological, oldest first — the same shape as GetConfirmedTimeline so
// downstream builders can share code.
//
// FY is derived at query time: the WHERE clause selects payslips whose
// (pay_period_year, pay_period_month) falls in the FY window. No schema
// migration needed.
func (s *Store) GetFYConfirmedTimeline(ctx context.Context, fyStartYear int) ([]Payslip, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.employer_name, p.pay_period_month, p.pay_period_year,
		       p.employee_id, p.designation, p.pay_days, p.total_days,
		       p.gross_salary, p.total_deductions, p.net_pay,
		       p.status, p.raw_pdf_path, p.created_at, COALESCE(p.confirmed_at, ''),
		       c.id, c.payslip_id, c.canonical_id, c.raw_label, c.amount, c.ytd_amount, c.category
		FROM payslips p
		LEFT JOIN payslip_components c ON c.payslip_id = p.id
		WHERE p.status = 'confirmed'
		  AND (
		        (p.pay_period_year = ?   AND p.pay_period_month >= 4)
		     OR (p.pay_period_year = ?+1 AND p.pay_period_month <= 3)
		  )
		ORDER BY p.pay_period_year ASC, p.pay_period_month ASC, p.id ASC,
		         CASE c.category WHEN 'earning' THEN 0 ELSE 1 END, c.raw_label`,
		fyStartYear, fyStartYear)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Same scan + grouping pattern as GetConfirmedTimeline; the only
	// difference is the WHERE filter above.
	byID := map[int64]*Payslip{}
	var order []int64
	for rows.Next() {
		var (
			p          Payslip
			status     string
			cID        sql.NullInt64
			cPayslipID sql.NullInt64
			cCanonID   sql.NullInt64
			cRawLabel  sql.NullString
			cAmount    sql.NullFloat64
			cYTD       sql.NullFloat64
			cCategory  sql.NullString
		)
		if err := rows.Scan(
			&p.ID, &p.EmployerName, &p.PayPeriodMonth, &p.PayPeriodYear,
			&p.EmployeeID, &p.Designation, &p.PayDays, &p.TotalDays,
			&p.GrossSalary, &p.TotalDeductions, &p.NetPay,
			&status, &p.RawPDFPath, &p.CreatedAt, &p.ConfirmedAt,
			&cID, &cPayslipID, &cCanonID, &cRawLabel, &cAmount, &cYTD, &cCategory,
		); err != nil {
			return nil, err
		}
		p.Status = Status(status)

		ptr, ok := byID[p.ID]
		if !ok {
			ptr = &p
			byID[p.ID] = ptr
			order = append(order, p.ID)
		}
		if cID.Valid {
			ptr.Components = append(ptr.Components, Component{
				ID:          cID.Int64,
				PayslipID:   cPayslipID.Int64,
				CanonicalID: cCanonID.Int64,
				RawLabel:    cRawLabel.String,
				Amount:      cAmount.Float64,
				YTDAmt:      cYTD.Float64,
				Category:    Category(cCategory.String),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Payslip, len(order))
	for i, id := range order {
		out[i] = *byID[id]
	}
	return out, nil
}

// GetYoYPair returns the confirmed payslip with the given ID (latest) plus
// the confirmed payslip from the previous financial year that shares the same
// pay_period_month (prevFY). Both have their Components populated. Returns
// ErrNotFound if the latest payslip doesn't exist, isn't confirmed, or has
// no same-month counterpart in the previous FY.
//
// "Previous financial year" is derived from the latest payslip's
// (pay_period_month, pay_period_year): if the latest is in FY (Y, Y+1), the
// previous FY is (Y-1, Y). The same-month counterpart must fall in that
// previous FY window. This matches the view layer's FinancialYear derivation
// so the store and view agree on what "previous FY" means.
func (s *Store) GetYoYPair(ctx context.Context, latestPayslipID int64) (latest, prevFY Payslip, err error) {
	// Fetch the latest payslip with components. Reuse GetPayslip for the
	// single-row + components load — it already returns ErrNotFound.
	latest, err = s.GetPayslip(ctx, latestPayslipID)
	if err != nil {
		return Payslip{}, Payslip{}, err
	}
	if latest.Status != StatusConfirmed {
		return Payslip{}, Payslip{}, ErrNotFound
	}

	// Derive the previous FY window from the latest payslip's period.
	latestFYStart := financialYearStart(latest.PayPeriodMonth, latest.PayPeriodYear)
	prevFYStart := latestFYStart - 1

	// Find the same-month confirmed payslip in the previous FY. There could
	// be multiple (different employers, duplicate uploads); pick the newest
	// by ID as the "canonical" prev-FY comparison point.
	row := s.db.QueryRowContext(ctx, `
		SELECT id FROM payslips
		WHERE status = 'confirmed'
		  AND pay_period_month = ?
		  AND (
		        (pay_period_year = ?   AND pay_period_month >= 4)
		     OR (pay_period_year = ?+1 AND pay_period_month <= 3)
		  )
		ORDER BY id DESC
		LIMIT 1`,
		latest.PayPeriodMonth, prevFYStart, prevFYStart)
	var prevID int64
	if err := row.Scan(&prevID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Payslip{}, Payslip{}, ErrNotFound
		}
		return Payslip{}, Payslip{}, err
	}

	prevFY, err = s.GetPayslip(ctx, prevID)
	if err != nil {
		return Payslip{}, Payslip{}, err
	}
	return latest, prevFY, nil
}

// financialYearStart is the store-layer mirror of the view layer's
// FinancialYear function: month ≥ 4 → FY starts in this year; month ≤ 3 →
// FY started last year. Kept here so the store doesn't depend on the web
// package for a one-line derivation.
func financialYearStart(month, year int) int {
	if month >= 4 {
		return year
	}
	return year - 1
}

// RawLabelUsage pairs a raw label with the number of confirmed payslips that
// used it for a given canonical. Shown on the component drill-down page so
// the user can see what payslip wording was mapped to this canonical.
type RawLabelUsage struct {
	RawLabel string
	Count    int
	LastSeen string // pay_period_year-month of most recent use, "" if none
}

// GetRawLabelUsage returns the raw labels mapped to canonicalID across all
// confirmed payslips, ordered by frequency then alphabetically. Empty for a
// canonical that has never been used.
func (s *Store) GetRawLabelUsage(ctx context.Context, canonicalID int64) ([]RawLabelUsage, error) {
	q := `
		SELECT c.raw_label,
		       COUNT(*) AS n,
		       COALESCE(MAX(printf('%04d-%02d', p.pay_period_year, p.pay_period_month)), '') AS last_seen
		FROM payslip_components c
		JOIN payslips p ON p.id = c.payslip_id
		WHERE c.canonical_id = ? AND p.status = 'confirmed'
		GROUP BY c.raw_label
		ORDER BY n DESC, c.raw_label ASC`
	rows, err := s.db.QueryContext(ctx, q, canonicalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RawLabelUsage
	for rows.Next() {
		var u RawLabelUsage
		if err := rows.Scan(&u.RawLabel, &u.Count, &u.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetComponentTimeline returns chronological (year, month, amount, ytd) points
// for one canonical component, across all confirmed payslips for the employer
// if employer is non-empty. Pending payslips are excluded to keep the series clean.
func (s *Store) GetComponentTimeline(ctx context.Context, canonicalID int64, employer string) ([]ComponentPoint, error) {
	q := `
		SELECT p.id, p.pay_period_year, p.pay_period_month, c.amount, c.ytd_amount, c.raw_label
		FROM payslip_components c
		JOIN payslips p ON p.id = c.payslip_id
		WHERE c.canonical_id = ? AND p.status = 'confirmed'`
	args := []any{canonicalID}
	if employer != "" {
		q += " AND p.employer_name = ?"
		args = append(args, employer)
	}
	q += " ORDER BY p.pay_period_year ASC, p.pay_period_month ASC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComponentPoint
	for rows.Next() {
		var pt ComponentPoint
		if err := rows.Scan(&pt.PayslipID, &pt.PayPeriodYear, &pt.PayPeriodMonth, &pt.Amount, &pt.YTDAmt, &pt.RawLabel); err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}
