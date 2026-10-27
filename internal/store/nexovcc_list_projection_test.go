package store

import (
	"reflect"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccPageProjectionPreservesFinances(t *testing.T) {
	s := transferStore(t)
	card := nexStoreCard(t, s)
	local, secret, err := s.GPTPayCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	limit := 5
	local.MaxAccounts = &limit
	if _, err = s.SaveGPTPayCard(local, secret); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, email := range []string{"first@example.com", "second@example.com", "first@example.com", "third@example.com"} {
		transferSource(t, s, email)
		id := []string{"first-original", "second-original", "first-repeat", "third-original"}[i]
		o := transferOrder(email, id, "success")
		o.Order.CardID, o.Order.CreatedAt = card.ID, now.Add(time.Duration(i)*time.Second)
		if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal(err)
		}
		if err := s.SetNexovccCase(id, "returned", "fixture refund confirmed", 0); err != nil {
			t.Fatal(err)
		}
	}
	f := nexovcc.Finance{At: now, Funds: []nexovcc.Fund{
		{ID: "opening", Serial: card.Serial, Kind: 3003, Currency: "USD", Amount: "-0.50"},
		{ID: "monthly", Serial: card.Serial, Kind: 3004, Currency: "USD", Amount: "-0.20"},
	}}
	if err := s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO nexovcc_revenue(identity,email,amount,updated_at) VALUES(?,?,?,?)`, "first-account", "first@example.com", 12.5, formatTime(now)); err != nil {
		t.Fatal(err)
	}
	full, err := s.NexovccCases()
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.NexovccCasesForEmails([]string{" FIRST@example.com "})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatal(page)
	}
	byID := map[string]NexovccCase{}
	for _, v := range full {
		byID[v.ID] = v
	}
	for _, v := range page {
		if !reflect.DeepEqual(v, byID[v.ID]) {
			t.Fatalf("filtered financial projection differs: %s\n%+v\n%+v", v.ID, v, byID[v.ID])
		}
		if v.SharedFees == nil || v.SharedFees.Count != 3 {
			t.Fatal("page changed shared-fee divisor", v)
		}
	}
	if page[0].SharedFees.Applied || !page[1].SharedFees.Applied || *page[0].Revenue != 0 || *page[1].Revenue != 12.5 {
		t.Fatal("repeated orders double-count fees or revenue", page)
	}
	if empty, e := s.NexovccCasesForEmails(nil); e != nil || len(empty) != 0 {
		t.Fatal(empty, e)
	}
}
