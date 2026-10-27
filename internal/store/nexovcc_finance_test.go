package store

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccReopenRebuildsHistoricalRefundMatches(t *testing.T) {
	for _, tc := range []struct {
		category   string
		settledUSD bool
	}{
		{"normal", false}, {"normal_pending", false}, {"closed-manual-returned", false},
		{"normal", true}, {"normal_pending", true}, {"closed-manual-returned", true},
	} {
		category := tc.category
		name := category
		if tc.settledUSD {
			name += "-usd-settlement"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			card := nexStoreCard(t, s)
			raw, err := os.ReadFile("../nexovcc/testdata/refund-descriptors.json")
			if err != nil {
				t.Fatal(err)
			}
			var f nexovcc.Finance
			if err = json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			if tc.settledUSD {
				for _, i := range []int{0, 2} {
					f.Trades[i].MerchantCurrency = "USD"
					f.Trades[i].MerchantAmount = f.Trades[i].Amount
				}
			}
			for i := range f.Trades {
				f.Trades[i].Serial = card.Serial
			}
			for i := range f.Funds {
				f.Funds[i].Serial = card.Serial
			}
			email := "historical-refund@example.com"
			transferSource(t, s, email)
			o := transferOrder(email, "historical-refund", "success")
			o.Order.CardID, o.Order.CreatedAt = card.ID, f.Trades[3].At
			o.Order.Remote.PaymentCurrency, o.Order.Remote.PaymentAmount = "USD", "141.82"
			if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			before := f
			before.Trades = f.Trades[1:]
			if err = s.SaveNexovccFinance(before); err != nil {
				t.Fatal(err)
			}
			// Reproduce an old parser's cached match alongside the complete issuer
			// evidence. Reopening must repair it without an upstream refresh.
			fullRaw := mustJSON(f)
			if err = s.NexovccCache("finance", f); err != nil {
				t.Fatal(err)
			}
			if category == "normal_pending" {
				if _, err = s.SetNexovccCardNormalPending(card.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if category == "closed-manual-returned" {
				card.Status = "4"
				if _, err = s.SaveNexovccCard(card, nil); err != nil {
					t.Fatal(err)
				}
				if err = s.SetNexovccCase(o.Order.ID, "returned", "upstream refund confirmed manually", 0); err != nil {
					t.Fatal(err)
				}
			}
			var oldReturned bool
			if err = s.db.QueryRow(`SELECT returned FROM nexovcc_case_matches WHERE order_id=?`, o.Order.ID).Scan(&oldReturned); err != nil || oldReturned {
				t.Fatal("fixture must have stale persisted match", oldReturned, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			cases, err := s.NexovccCases()
			if err != nil || len(cases) != 1 || cases[0].State != "returned" || cases[0].Match == nil {
				t.Fatal(cases, err)
			}
			g := cases[0].Match.Group
			if !g.Valid || !g.FullyReturned || g.Returned != "141.23" || g.Cost != "7.75" || g.ReturnedAt.IsZero() {
				t.Fatalf("history not recalculated: %+v", g)
			}
			var persisted bool
			var cached string
			if err = s.db.QueryRow(`SELECT returned FROM nexovcc_case_matches WHERE order_id=?`, o.Order.ID).Scan(&persisted); err != nil || !persisted {
				t.Fatal("admission flag remains stale", persisted, err)
			}
			if err = s.db.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&cached); err != nil || cached != fullRaw {
				t.Fatal("reconciliation changed issuer evidence or sync timestamps", err)
			}
			if category == "closed-manual-returned" && (cases[0].ManualState != "returned" || cases[0].Reason != "upstream refund confirmed manually") {
				t.Fatal("manual history changed", cases[0])
			}
			if err = s.reconcileNexovccFinance(); err != nil {
				t.Fatal("repeated reconciliation", err)
			}
		})
	}
}

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

func TestNexovccCrossAuthorizationRefundMarksCaseReturnedAndKeepsCosts(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	local, secret, err := s.GPTPayCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	limit := 5
	local.MaxAccounts = &limit
	if _, err = s.SaveGPTPayCard(local, secret); err != nil {
		t.Fatal(err)
	}
	email := "refund-4276@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "refund-4276-order", "success")
	o.Order.CardID = c.ID
	o.Order.CreatedAt = time.Now().Add(-time.Hour)
	o.Order.Remote.PaymentCurrency = "USD"
	o.Order.Remote.PaymentAmount = "141.82"
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	at := o.Order.CreatedAt.Add(time.Minute)
	f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{
		{ID: "payment-4276", Serial: c.Serial, Authorization: "UX0VKI", Currency: "USD", Amount: "141.82", MerchantAmount: "8919.64", MerchantCurrency: "PHP", Merchant: "OPENAI*CHATGPTSUBSCR", Status: "COMPLETE", At: at},
		{ID: "refund-4276", Serial: c.Serial, Authorization: "3JWWV3", Currency: "USD", Amount: "141.23", MerchantAmount: "8898.46", MerchantCurrency: "PHP", Merchant: "OPENAI*CHATGPTSUBSCR", Status: "REFUNDED", At: at.Add(42 * time.Minute)},
	}, Funds: []nexovcc.Fund{
		{ID: "payment-fee", Serial: c.Serial, Link: "payment-4276", Currency: "USD", Kind: 2007, Amount: "-2.42"},
		{ID: "refund-fee", Serial: c.Serial, Link: "refund-4276", Currency: "USD", Kind: 8888, Amount: "-4.74"},
	}}
	if err = s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	cases, err := s.NexovccCases()
	if err != nil || len(cases) != 1 || cases[0].State != "returned" || cases[0].Match == nil || !cases[0].Match.Group.FullyReturned {
		t.Fatalf("a completed unique refund must release the Pro payment: %+v (%v)", cases, err)
	}
	g := cases[0].Match.Group
	if g.Authorization != "UX0VKI" || g.Payment != "141.82" || g.Returned != "141.23" || g.Difference != "0.59" || g.Fees != "7.16" || g.Cost != "7.75" || len(g.TradeIDs) != 2 {
		t.Fatalf("refund principal difference or fees were lost: %+v", g)
	}
	var returned bool
	if err = s.db.QueryRow(`SELECT returned FROM nexovcc_case_matches WHERE order_id=?`, o.Order.ID).Scan(&returned); err != nil || !returned {
		t.Fatalf("persisted match did not permit later withdrawal/recycle checks: returned=%v err=%v", returned, err)
	}
	if err = s.ReserveProCard("next-refund@example.com", c.ID, false); err != nil {
		t.Fatalf("confirmed refund did not release the card: %v", err)
	}
}
