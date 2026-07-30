package store

import (
	"context"
	"testing"
)

func TestTaxpayerProfile_EmptyDB_ReturnsNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetTaxpayerProfile(context.Background())
	if err != ErrNotFound {
		t.Errorf("GetTaxpayerProfile on empty DB: err = %v, want ErrNotFound", err)
	}
}

func TestTaxpayerProfile_SaveThenGet_Roundtrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	want := TaxpayerProfile{
		PAN:               "ABCDE1234F",
		DOB:               "15061990",
		DeclarantName:     "Udit Mittal",
		VerificationPlace: "Bangalore",
	}
	if err := st.SaveTaxpayerProfile(ctx, want); err != nil {
		t.Fatalf("SaveTaxpayerProfile: %v", err)
	}

	got, err := st.GetTaxpayerProfile(ctx)
	if err != nil {
		t.Fatalf("GetTaxpayerProfile: %v", err)
	}
	if got.PAN != want.PAN || got.DOB != want.DOB || got.DeclarantName != want.DeclarantName || got.VerificationPlace != want.VerificationPlace {
		t.Errorf("GetTaxpayerProfile = %+v, want %+v", got, want)
	}
}

func TestTaxpayerProfile_SaveTwice_Overwrites(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_ = st.SaveTaxpayerProfile(ctx, TaxpayerProfile{PAN: "OLD1234A", DOB: "01011990"})
	_ = st.SaveTaxpayerProfile(ctx, TaxpayerProfile{PAN: "NEW5678B", DOB: "02021991", DeclarantName: "Updated", VerificationPlace: "Mumbai"})

	got, err := st.GetTaxpayerProfile(ctx)
	if err != nil {
		t.Fatalf("GetTaxpayerProfile: %v", err)
	}
	if got.PAN != "NEW5678B" {
		t.Errorf("PAN = %q, want %q (should be overwritten)", got.PAN, "NEW5678B")
	}
	if got.DeclarantName != "Updated" {
		t.Errorf("DeclarantName = %q, want %q", got.DeclarantName, "Updated")
	}
}

func TestBankAccounts_EmptyDB_ReturnsEmpty(t *testing.T) {
	st := newTestStore(t)
	accounts, err := st.ListBankAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListBankAccounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("ListBankAccounts on empty DB: %d accounts, want 0", len(accounts))
	}
}

func TestBankAccounts_SaveAndList(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id1, err := st.SaveBankAccount(ctx, BankAccount{
		IFSC: "HDFC0000123", AccountNumber: "1234567890", AccountType: "savings", BankName: "HDFC", IsPrimary: true,
	})
	if err != nil {
		t.Fatalf("SaveBankAccount 1: %v", err)
	}
	if id1 == 0 {
		t.Error("SaveBankAccount returned id=0")
	}

	_, err = st.SaveBankAccount(ctx, BankAccount{
		IFSC: "ICIC0000456", AccountNumber: "9876543210", AccountType: "current", BankName: "ICICI", IsPrimary: false,
	})
	if err != nil {
		t.Fatalf("SaveBankAccount 2: %v", err)
	}

	accounts, err := st.ListBankAccounts(ctx)
	if err != nil {
		t.Fatalf("ListBankAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("ListBankAccounts: %d accounts, want 2", len(accounts))
	}
	if !accounts[0].IsPrimary {
		t.Error("first account should be primary (ordered first)")
	}
	if accounts[1].IsPrimary {
		t.Error("second account should not be primary")
	}
}

func TestBankAccounts_OnlyOnePrimaryAtATime(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, _ = st.SaveBankAccount(ctx, BankAccount{IFSC: "A", AccountNumber: "1", AccountType: "savings", IsPrimary: true})
	_, _ = st.SaveBankAccount(ctx, BankAccount{IFSC: "B", AccountNumber: "2", AccountType: "savings", IsPrimary: true})

	accounts, _ := st.ListBankAccounts(ctx)
	primaryCount := 0
	for _, a := range accounts {
		if a.IsPrimary {
			primaryCount++
		}
	}
	if primaryCount != 1 {
		t.Errorf("after saving two primaries, %d primary accounts, want 1", primaryCount)
	}
}

func TestBankAccounts_SetPrimary(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id1, _ := st.SaveBankAccount(ctx, BankAccount{IFSC: "A", AccountNumber: "1", AccountType: "savings", IsPrimary: true})
	id2, _ := st.SaveBankAccount(ctx, BankAccount{IFSC: "B", AccountNumber: "2", AccountType: "savings", IsPrimary: false})

	if err := st.SetPrimaryBankAccount(ctx, id2); err != nil {
		t.Fatalf("SetPrimaryBankAccount: %v", err)
	}

	accounts, _ := st.ListBankAccounts(ctx)
	for _, a := range accounts {
		if a.ID == id1 && a.IsPrimary {
			t.Error("account 1 should no longer be primary")
		}
		if a.ID == id2 && !a.IsPrimary {
			t.Error("account 2 should now be primary")
		}
	}
}

func TestBankAccounts_SetPrimary_NonexistentID(t *testing.T) {
	st := newTestStore(t)
	err := st.SetPrimaryBankAccount(context.Background(), 9999)
	if err != ErrNotFound {
		t.Errorf("SetPrimaryBankAccount(9999): err = %v, want ErrNotFound", err)
	}
}

func TestBankAccounts_Delete(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, _ := st.SaveBankAccount(ctx, BankAccount{IFSC: "A", AccountNumber: "1", AccountType: "savings", IsPrimary: true})

	if err := st.DeleteBankAccount(ctx, id); err != nil {
		t.Fatalf("DeleteBankAccount: %v", err)
	}

	accounts, _ := st.ListBankAccounts(ctx)
	if len(accounts) != 0 {
		t.Errorf("after delete, %d accounts, want 0", len(accounts))
	}
}

func TestBankAccounts_DeletePrimary_PromotesNext(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id1, _ := st.SaveBankAccount(ctx, BankAccount{IFSC: "A", AccountNumber: "1", AccountType: "savings", IsPrimary: true})
	id2, _ := st.SaveBankAccount(ctx, BankAccount{IFSC: "B", AccountNumber: "2", AccountType: "savings", IsPrimary: false})

	_ = st.DeleteBankAccount(ctx, id1)

	accounts, _ := st.ListBankAccounts(ctx)
	if len(accounts) != 1 {
		t.Fatalf("after deleting primary, %d accounts, want 1", len(accounts))
	}
	if accounts[0].ID != id2 {
		t.Errorf("remaining account id = %d, want %d", accounts[0].ID, id2)
	}
	if !accounts[0].IsPrimary {
		t.Error("remaining account should be promoted to primary")
	}
}

func TestBankAccounts_Delete_NonexistentID(t *testing.T) {
	st := newTestStore(t)
	err := st.DeleteBankAccount(context.Background(), 9999)
	if err != ErrNotFound {
		t.Errorf("DeleteBankAccount(9999): err = %v, want ErrNotFound", err)
	}
}
