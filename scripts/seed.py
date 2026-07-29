#!/usr/bin/env python3
"""Clear payslips & components, then insert dummy data for demo.

Usage: python3 scripts/seed.py

Requires: sqlite3 (stdlib)
"""

import sqlite3
import os
import random
import math
from pathlib import Path

DB = Path.home() / ".cresto" / "income.db"

# Canonical names the store seeds on first run
CANONICAL_NAMES = [
    "basic", "hra", "da", "conveyance", "medical", "lta", "education",
    "telephone", "special_allowance", "bonus", "arrears", "leave_encashment",
    "other_earnings", "epf", "professional_tax", "tds", "esi", "lwf", "lop",
    "loan_recovery", "other_deductions",
]


def main():
    if not DB.exists():
        print(f"DB not found: {DB}")
        print("Run the app once (cresto serve) to create it.")
        return 1

    conn = sqlite3.connect(str(DB))
    cur = conn.cursor()

    # Look up canonical IDs
    cur.execute("SELECT id, name FROM canonicals ORDER BY id")
    rows = cur.fetchall()
    can_by_name = {name: cid for cid, name in rows}

    missing = [n for n in CANONICAL_NAMES if n not in can_by_name]
    if missing:
        print(f"Missing canonicals: {missing}")
        return 1

    print(f"Found {len(can_by_name)} canonicals (IDs {min(can_by_name.values())}-{max(can_by_name.values())})")

    # Clear existing data
    cur.execute("DELETE FROM payslip_components")
    cur.execute("DELETE FROM payslips")
    cur.execute("DELETE FROM upload_batches")
    conn.commit()

    # Acme Corp: Jan 2023 – Jun 2026
    for year in range(2023, 2027):
        end = 6 if year == 2026 else 12
        for month in range(1, end + 1):
            status = "pending_review" if year == 2026 and month >= 4 else "confirmed"
            _insert_payslip(conn, cur, can_by_name, "Acme Corp", year, month, 80_000, status)

    # TechStart Inc: Jan 2023 – Mar 2024
    for year in range(2023, 2025):
        end = 3 if year == 2024 else 12
        for month in range(1, end + 1):
            _insert_payslip(conn, cur, can_by_name, "TechStart Inc", year, month, 120_000, "confirmed")

    # One failed payslip
    _insert_payslip(conn, cur, can_by_name, "Acme Corp", 2026, 7, 80_000, "failed")
    # One processing payslip
    _insert_payslip(conn, cur, can_by_name, "Acme Corp", 2026, 8, 80_000, "processing")

    conn.commit()

    total = cur.execute("SELECT COUNT(*) FROM payslips").fetchone()[0]
    print(f"Seeded {total} payslips")

    conn.close()


def _insert_payslip(conn, cur, can, employer, year, month, basic, status):
    rng = random.Random(year * 1000 + month * 7 + len(employer) * 3)

    hra = round(basic * 0.4)
    da = round(basic * 0.05)
    conveyance = 1600.0
    medical = 1250.0
    special = round(basic * 0.15)

    add_bonus = rng.randint(0, 2) == 0
    if add_bonus:
        special += 5000

    epf = round(basic * 0.12)
    prof_tax = 200.0
    tds = round((basic + hra + da + conveyance + medical + special) * 0.08)
    lop = 0.0
    if rng.randint(0, 9) < 2:
        lop = round(basic / 30 * 2)

    gross = basic + hra + da + conveyance + medical + special
    total_ded = epf + prof_tax + tds + lop
    net_pay = round(gross - total_ded)
    pay_days = 30 - (2 if lop > 0 else 0)

    err_msg = "Could not parse: unsupported PDF format" if status == "failed" else ""

    cur.execute("""
        INSERT INTO payslips
            (employer_name, pay_period_month, pay_period_year,
             employee_id, designation, pay_days, total_days,
             gross_salary, total_deductions, net_pay,
             status, raw_pdf_path, batch_id, error_message)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    """, (employer, month, year, f"EMP{basic//1000:03d}", "Software Engineer",
          pay_days, 30, gross, total_ded, net_pay,
          status, f"[PDF {year}{month:02d}]", "", err_msg))

    pid = cur.lastrowid

    comps = [
        (pid, can["basic"], "Basic", basic, basic, "earning"),
        (pid, can["hra"], "HRA", hra, hra, "earning"),
        (pid, can["da"], "Dearness Allowance", da, da, "earning"),
        (pid, can["conveyance"], "Conveyance", conveyance, conveyance, "earning"),
        (pid, can["medical"], "Medical", medical, medical, "earning"),
        (pid, can["special_allowance"], "Special Allowance", special, special, "earning"),
        (pid, can["epf"], "EPF", epf, epf, "deduction"),
        (pid, can["professional_tax"], "Professional Tax", prof_tax, prof_tax, "deduction"),
        (pid, can["tds"], "TDS", tds, tds, "deduction"),
    ]
    if add_bonus:
        comps.append((pid, can["bonus"], "Bonus", 5000, 5000, "earning"))
    if lop > 0:
        comps.append((pid, can["lop"], "LOP", lop, lop, "deduction"))

    cur.executemany("""
        INSERT INTO payslip_components
            (payslip_id, canonical_id, raw_label, amount, ytd_amount, category)
        VALUES (?, ?, ?, ?, ?, ?)
    """, comps)


if __name__ == "__main__":
    exit(main())
