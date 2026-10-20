package store

import (
	"fmt"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccSharedFeesFollowAllSuccessfulAccounts(t *testing.T) {
	s := transferStore(t)
	card := nexStoreCard(t, s)
	finance := nexovcc.Finance{At: time.Now(), Funds: []nexovcc.Fund{
		{ID: "opening", Serial: card.Serial, Kind: 3003, Currency: "USD", Amount: "-0.50"},
		{ID: "monthly", Serial: card.Serial, Kind: 3004, Currency: "USD", Amount: "-0.20"},
		{ID: "other-card", Serial: "other", Kind: 3003, Currency: "USD", Amount: "-99.00"},
	}}
	if err := s.SaveNexovccFinance(finance); err != nil {
		t.Fatal(err)
	}
	cases := []NexovccCase{}
	add := func(id, email, status string) {
		cases = append([]NexovccCase{{ID: id, Email: email, CardID: card.ID, PaymentStatus: status, Deleted: true}}, cases...)
	}
	add("failed-order", "failed@example.com", "failed")
	add("pending-order", "pending@example.com", "submission_unknown")
	for n, want := range []string{"0.50", "0.25", "0.16"} {
		add(fmt.Sprintf("ok-%d", n), fmt.Sprintf("account%d@example.com", n), "success")
		err := allocateNexovccCardFees(s.db, cases, finance)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			if c.PaymentStatus != "success" {
				if c.SharedFees != nil {
					t.Fatal("failed/pending participates", c)
				}
				continue
			}
			a := c.SharedFees
			if a == nil || !a.Ready || !a.Applied || a.Count != n+1 || a.Opening != want || a.OpeningTotal != "0.50" || a.MonthlyTotal != "0.20" {
				t.Fatal(c)
			}
			if a.Monthly != []string{"0.20", "0.10", "0.06"}[n] {
				t.Fatal(a)
			}
		}
	}
	add("repeat-order", "ACCOUNT0@example.com", "success")
	err := allocateNexovccCardFees(s.db, cases, finance)
	if err != nil {
		t.Fatal(err)
	}
	var count, sum int
	for _, c := range cases {
		if c.PaymentStatus != "success" {
			continue
		}
		if c.SharedFees.Count != 3 {
			t.Fatal("repeated email changed divisor", c)
		}
		if c.SharedFees.Applied {
			count++
		}
		n, _ := nexovcc.Cents(c.SharedFees.Opening)
		sum += int(n)
	}
	if count != 3 || sum != 48 {
		t.Fatal("share duplicated or rounded upward", count, sum)
	}
	finance.Funds = append(finance.Funds, nexovcc.Fund{ID: "month2", Serial: card.Serial, Kind: 3004, Currency: "USD", Amount: "-0.20"})
	if err := s.SaveNexovccFinance(finance); err != nil {
		t.Fatal(err)
	}
	err = allocateNexovccCardFees(s.db, cases, finance)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.SharedFees != nil && c.SharedFees.Applied && c.SharedFees.Monthly != "0.13" {
			t.Fatal("new monthly charge not reflected", c)
		}
	}
	var saved nexovcc.Finance
	if err := s.NexovccCached("finance", &saved); err != nil || saved.Funds[0].Amount != "-0.50" {
		t.Fatal("allocation mutated ledger", err)
	}
}
