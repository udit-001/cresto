package web

import (
	"strings"
	"testing"
)

func TestSettings_TaxProfile_EmptyPage_HasForm(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/settings")
	body := rec.Body.String()
	if !strings.Contains(body, "Tax Profile") {
		t.Error("settings page should have a Tax Profile section")
	}
	if !strings.Contains(body, `name="pan"`) {
		t.Error("settings page should have a PAN input field")
	}
}

func TestSettings_TaxProfile_SaveThenLoad(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec := doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=1990-06-15&declarant_name=John+Doe&verification_place=Anytown")
	if rec.Code != 303 {
		t.Fatalf("POST tax-profile: status = %d, want 303", rec.Code)
	}

	rec, _ = doGet(srv, "/settings")
	body := rec.Body.String()
	if !strings.Contains(body, "ABCDE1234F") {
		t.Error("settings page should show saved PAN")
	}
	if !strings.Contains(body, "1990-06-15") {
		t.Error("settings page should show saved DOB as ISO date")
	}
	if !strings.Contains(body, "John Doe") {
		t.Error("settings page should show saved declarant name")
	}
}

func TestSettings_TaxProfile_Overwrite(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=OLD1234A&dob=1990-01-01&declarant_name=Old&verification_place=OldCity")
	doPostForm(srv, "/settings/tax-profile",
		"pan=NEW5678B&dob=1991-02-02&declarant_name=New&verification_place=NewCity")

	rec, _ := doGet(srv, "/settings")
	body := rec.Body.String()
	if strings.Contains(body, "OLD1234A") {
		t.Error("old PAN should be overwritten, not visible")
	}
	if !strings.Contains(body, "NEW5678B") {
		t.Error("new PAN should be visible")
	}
}

func TestSettings_BankAccounts_EmptyPage_HasAddForm(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/settings")
	body := rec.Body.String()
	if !strings.Contains(body, "Bank Accounts") {
		t.Error("settings page should have a Bank Accounts section")
	}
	if !strings.Contains(body, `name="ifsc"`) {
		t.Error("settings page should have an add bank account form (IFSC field)")
	}
}

func TestSettings_BankAccounts_AddThenDisplay(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec := doPostForm(srv, "/settings/bank-account",
		"ifsc=HDFC0000123&account_number=1234567890&account_type=savings&bank_name=HDFC+Bank&is_primary=on")
	if rec.Code != 303 {
		t.Fatalf("POST bank-account: status = %d, want 303", rec.Code)
	}

	rec, _ = doGet(srv, "/settings")
	body := rec.Body.String()
	if !strings.Contains(body, "HDFC Bank") {
		t.Error("settings page should show bank name")
	}
	if !strings.Contains(body, "HDFC0000123") {
		t.Error("settings page should show IFSC")
	}
	if !strings.Contains(body, "1234567890") {
		t.Error("settings page should show account number")
	}
	if !strings.Contains(body, "Primary") {
		t.Error("settings page should show Primary badge for the primary account")
	}
}

func TestSettings_BankAccounts_AddMultiple_SetPrimary(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Add two accounts, first as primary.
	doPostForm(srv, "/settings/bank-account",
		"ifsc=BANK001&account_number=111111&account_type=savings&bank_name=Bank+One&is_primary=on")
	doPostForm(srv, "/settings/bank-account",
		"ifsc=BANK002&account_number=222222&account_type=savings&bank_name=Bank+Two")

	// Verify first is primary.
	rec, _ := doGet(srv, "/settings")
	body := rec.Body.String()
	if !strings.Contains(body, "Bank One") || !strings.Contains(body, "Bank Two") {
		t.Fatal("both bank accounts should be listed")
	}

	// Set second as primary.
	rec = doPostForm(srv, "/settings/bank-account/2/primary", "")
	if rec.Code != 303 {
		t.Fatalf("POST set-primary: status = %d, want 303", rec.Code)
	}

	// Verify Bank Two is now primary and appears before Bank One
	// (primary accounts are ordered first by the store).
	rec, _ = doGet(srv, "/settings")
	body = rec.Body.String()
	oneIdx := strings.Index(body, "Bank One")
	twoIdx := strings.Index(body, "Bank Two")
	if oneIdx == -1 || twoIdx == -1 {
		t.Fatal("both accounts should be present")
	}
	if twoIdx > oneIdx {
		t.Error("Bank Two (primary) should appear before Bank One in the list")
	}
	// Check Bank Two has Primary badge.
	twoSection := body[twoIdx:]
	if !strings.Contains(twoSection[:200], "Primary") {
		t.Error("Bank Two should have Primary badge after set-primary")
	}
}

func TestSettings_BankAccounts_Delete(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/bank-account",
		"ifsc=BANK001&account_number=111111&account_type=savings&bank_name=Bank+One&is_primary=on")

	rec := doPostForm(srv, "/settings/bank-account/1/delete", "")
	if rec.Code != 303 {
		t.Fatalf("POST delete bank-account: status = %d, want 303", rec.Code)
	}

	rec, _ = doGet(srv, "/settings")
	body := rec.Body.String()
	if strings.Contains(body, "Bank One") {
		t.Error("deleted bank account should not appear on settings page")
	}
}

func TestSettings_BankAccounts_DeleteNonexistent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec := doPostForm(srv, "/settings/bank-account/9999/delete", "")
	if rec.Code != 404 {
		t.Errorf("delete nonexistent account: status = %d, want 404", rec.Code)
	}
}

func TestSettings_BankAccounts_AddWithoutIFSC_ReturnsError(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec := doPostForm(srv, "/settings/bank-account",
		"ifsc=&account_number=123&account_type=savings&bank_name=Test")
	if rec.Code != 400 {
		t.Errorf("add bank account without IFSC: status = %d, want 400", rec.Code)
	}
}
