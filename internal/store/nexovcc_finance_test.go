package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccCasePresentationMetadata(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	email := "metadata@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "metadata-order", "success")
	o.Order.CardID = c.ID
	o.Order.CardName = "环球主卡"
	o.Order.Provider = "cmsnav"
	opened := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	o.Order.SucceededAt = &opened
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	rows, err := s.NexovccCases()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	row := rows[0]
	if row.CardName != "环球主卡" || row.Provider != "cmsnav" || row.OpenedAt == nil || !row.OpenedAt.Equal(opened) || row.Deleted || row.ManualState != "" {
		t.Fatalf("incorrect metadata: %+v", row)
	}
	if err := s.SetNexovccCase(row.ID, "returned", "已核实人工回款状态", row.Revision); err != nil {
		t.Fatal(err)
	}
	rows, err = s.NexovccCases()
	if err != nil || rows[0].ManualState != "returned" || rows[0].UpdatedAt == "" || rows[0].Match != nil {
		t.Fatal("manual status must not fabricate returned transaction/time", rows, err)
	}
	if err := s.SetNexovccCase(row.ID, "auto", "恢复流水自动判断", rows[0].Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM mail_accounts WHERE email=?`, email); err != nil {
		t.Fatal(err)
	}
	rows, err = s.NexovccCases()
	if err != nil || len(rows) != 1 || !rows[0].Deleted || rows[0].ManualState != "" || rows[0].State != "opened" {
		t.Fatal("deleted account must retain order and automatic status", rows, err)
	}
}

func TestNexovccAutomaticReturnsAndAmbiguousMatching(t *testing.T) {
	for _, mode := range []string{"unique", "usd", "ambiguous", "unknown", "partial", "manual"} {
		t.Run(mode, func(t *testing.T) {
			s := transferStore(t)
			c := nexStoreCard(t, s)
			card, secret, _ := s.GPTPayCard(c.ID)
			limit := 5
			card.MaxAccounts = &limit
			if _, err := s.SaveGPTPayCard(card, secret); err != nil {
				t.Fatal(err)
			}
			transferSource(t, s, "return@example.com")
			o := transferOrder("return@example.com", "return-1", "success")
			o.Order.CardID = c.ID
			o.Order.CreatedAt = time.Now().Add(-time.Hour)
			if mode == "usd" {
				o.Order.Remote.PaymentCurrency = "USD"
				o.Order.Remote.PaymentAmount = "92.33"
			}
			if mode == "unknown" {
				o.Order.Status = "submission_unknown"
			}
			if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			if mode == "manual" {
				if err := s.SetNexovccCase(o.Order.ID, "waiting", "人工确认尚未收到回款", 0); err != nil {
					t.Fatal(err)
				}
			}
			f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{
				{ID: "paid", Serial: c.Serial, Authorization: "auth-1", Currency: "USD", Amount: "92.33", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "PENDING", At: o.Order.CreatedAt.Add(time.Minute)},
				{ID: "returned", Serial: c.Serial, Authorization: "auth-1", Currency: "USD", Amount: "92.28", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "REVERSED", At: o.Order.CreatedAt.Add(10 * time.Minute)},
			}}
			if mode == "ambiguous" {
				r := f.Trades[0]
				r.ID = "other"
				r.Authorization = "auth-other"
				f.Trades = append(f.Trades, r)
			}
			if mode == "partial" {
				f.Trades[1].MerchantAmount = "50.00"
				f.Trades[1].Amount = "1.00"
			}
			for i := 0; i < 2; i++ {
				if err := s.SaveNexovccFinance(f); err != nil {
					t.Fatal(err)
				}
			}
			cases, err := s.NexovccCases()
			if err != nil || len(cases) != 1 {
				t.Fatal(cases, err)
			}
			if mode == "unique" || mode == "usd" {
				if cases[0].State != "returned" || cases[0].Match == nil {
					t.Fatal(cases)
				}
				if err = s.ReserveProCard("next@example.com", c.ID, false); err != nil {
					t.Fatal(err)
				}
				_ = s.ReleaseProCard("next@example.com")
			} else if err = s.ReserveProCard("next@example.com", c.ID, false); err == nil {
				t.Fatal("released without unique confirmed return", mode)
			}
			if mode == "ambiguous" && cases[0].Match != nil {
				t.Fatal("ambiguous match assigned")
			}
			if mode == "unique" || mode == "usd" {
				now := time.Now()
				_, err = s.UpdateProAccount(o.Order.Email, func(p *model.MailAccountProfile) {
					p.CostConnectionIdentity = "connection"
					p.UserCostDownstreamIdentity = "123"
					p.UserCostDownstreamSnapshot = 20
					p.CostCheckedAt = &now
				})
				if err != nil {
					t.Fatal(err)
				}
				if err = s.CaptureNexovccRevenue(); err != nil {
					t.Fatal(err)
				}
				if err = s.CaptureNexovccRevenue(); err != nil {
					t.Fatal(err)
				}
				cases, _ = s.NexovccCases()
				if cases[0].Revenue == nil || *cases[0].Revenue != 20 {
					t.Fatal(cases)
				}
				if err = s.SetNexovccCase(o.Order.ID, "waiting", "人工复核暂停释放", 0); err != nil {
					t.Fatal(err)
				}
				if err = s.ReserveProCard("next@example.com", c.ID, false); err == nil {
					t.Fatal("automatic match bypassed manual override")
				}
			}
		})
	}
}

func TestNexovccResidualSettlementPreservesReturnedAccount(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	transferSource(t, s, "settlement@example.com")
	o := transferOrder("settlement@example.com", "settlement-order", "success")
	o.Order.CardID = c.ID
	o.Order.CreatedAt = time.Now().Add(-time.Hour)
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{
		{ID: "paid", Serial: c.Serial, Authorization: "auth", Currency: "USD", Amount: "92.33", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "PENDING", At: o.Order.CreatedAt.Add(time.Minute)},
		{ID: "return", Serial: c.Serial, Authorization: "auth", Currency: "USD", Amount: "92.28", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "REVERSED", At: o.Order.CreatedAt.Add(10 * time.Minute)},
	}}
	if err := s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	before, err := s.NexovccCases()
	if err != nil || len(before) != 1 || before[0].Match == nil || before[0].State != "returned" {
		t.Fatal(before, err)
	}
	f.Trades = append(f.Trades, nexovcc.Trade{ID: "settlement", Serial: c.Serial, Authorization: "auth", Currency: "USD", Amount: "0.05", MerchantAmount: "0.05", MerchantCurrency: "USD", Status: "COMPLETE", At: o.Order.CreatedAt.Add(30 * time.Minute)})
	if err = s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	after, err := s.NexovccCases()
	if err != nil || len(after) != 1 || after[0].Match == nil || after[0].State != "returned" || after[0].Match.Group.ID != before[0].Match.Group.ID || after[0].Match.Group.Payment != "92.33" || after[0].Match.Group.Cost != "0.05" {
		t.Fatal(after, err)
	}
}
