package store

import (
	"context"
	"testing"
)

func sampleCGTrades() []CapitalGainsTrade {
	return []CapitalGainsTrade{
		{Section: "Equity - Short Term", Symbol: "NETWEB", ISIN: "INE0NT", EntryDate: "2024-11-06", ExitDate: "2025-09-22", Quantity: 1, BuyValue: 2785.25, SellValue: 3465.60, Profit: 680.35, TaxableProfit: 680.35, STT: 3.46},
		{Section: "Equity - Short Term", Symbol: "SJS", ISIN: "INE284", EntryDate: "2024-12-02", ExitDate: "2025-08-20", Quantity: 12, BuyValue: 15453, SellValue: 16284, Profit: 831, TaxableProfit: 831, STT: 16.39},
		{Section: "Equity - Long Term", Symbol: "RELIANCE", ISIN: "INE002", EntryDate: "2023-01-15", ExitDate: "2025-12-10", Quantity: 10, BuyValue: 20000, SellValue: 25000, Profit: 5000, TaxableProfit: 3000, FMV: 22000, STT: 25},
	}
}

func TestCapitalGains_EmptyDB(t *testing.T) {
	st := newTestStore(t)
	trades, err := st.ListCapitalGainsTrades(context.Background(), 2025)
	if err != nil {
		t.Fatalf("ListCapitalGainsTrades: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("empty DB: got %d trades, want 0", len(trades))
	}
	has, _ := st.HasCapitalGains(context.Background(), 2025)
	if has {
		t.Error("HasCapitalGains on empty DB should be false")
	}
}

func TestCapitalGains_SaveThenList(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	trades := sampleCGTrades()
	if err := st.SaveCapitalGainsTrades(ctx, 2025, trades); err != nil {
		t.Fatalf("SaveCapitalGainsTrades: %v", err)
	}

	got, err := st.ListCapitalGainsTrades(ctx, 2025)
	if err != nil {
		t.Fatalf("ListCapitalGainsTrades: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d trades, want 3", len(got))
	}
	if got[0].Symbol != "RELIANCE" {
		t.Errorf("trades[0].Symbol = %q, want RELIANCE (Long Term sorts first)", got[0].Symbol)
	}
	if got[0].Section != "Equity - Long Term" {
		t.Errorf("trades[0].Section = %q", got[0].Section)
	}
	if got[2].Symbol != "SJS" {
		t.Errorf("trades[2].Symbol = %q, want SJS (Short Term, after NETWEB)", got[2].Symbol)
	}

	has, _ := st.HasCapitalGains(ctx, 2025)
	if !has {
		t.Error("HasCapitalGains after save should be true")
	}
}

func TestCapitalGains_Reimport_Replaces(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_ = st.SaveCapitalGainsTrades(ctx, 2025, sampleCGTrades())

	newTrades := []CapitalGainsTrade{
		{Section: "Equity - Short Term", Symbol: "NEWSTOCK", ISIN: "INE999", BuyValue: 1000, SellValue: 2000, TaxableProfit: 1000},
	}
	if err := st.SaveCapitalGainsTrades(ctx, 2025, newTrades); err != nil {
		t.Fatalf("SaveCapitalGainsTrades (reimport): %v", err)
	}

	got, _ := st.ListCapitalGainsTrades(ctx, 2025)
	if len(got) != 1 {
		t.Fatalf("after reimport: got %d trades, want 1 (old ones deleted)", len(got))
	}
	if got[0].Symbol != "NEWSTOCK" {
		t.Errorf("after reimport: trades[0].Symbol = %q, want NEWSTOCK", got[0].Symbol)
	}
}

func TestCapitalGains_DifferentFY(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_ = st.SaveCapitalGainsTrades(ctx, 2024, sampleCGTrades())
	_ = st.SaveCapitalGainsTrades(ctx, 2025, sampleCGTrades())

	got2024, _ := st.ListCapitalGainsTrades(ctx, 2024)
	got2025, _ := st.ListCapitalGainsTrades(ctx, 2025)
	if len(got2024) != 3 {
		t.Errorf("FY2024: got %d, want 3", len(got2024))
	}
	if len(got2025) != 3 {
		t.Errorf("FY2025: got %d, want 3", len(got2025))
	}
}

func TestCapitalGains_EmptyBatch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.SaveCapitalGainsTrades(ctx, 2025, nil); err != nil {
		t.Fatalf("SaveCapitalGainsTrades with nil: %v", err)
	}
	got, _ := st.ListCapitalGainsTrades(ctx, 2025)
	if len(got) != 0 {
		t.Errorf("empty batch: got %d, want 0", len(got))
	}
}
