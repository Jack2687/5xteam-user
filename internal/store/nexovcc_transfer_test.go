package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"testing"
	"time"
)

func TestNexovccProTransferPreservesReturnAndOccupancy(t *testing.T) {
	for _, state := range []string{"opened", "waiting", "returned", "unopened", "auto", "matched", "unknown"} {
		t.Run(state, func(t *testing.T) {
			src := transferStore(t)
			card := nexStoreCard(t, src)
			email := "nex-transfer@example.com"
			transferSource(t, src, email)
			o := transferOrder(email, "nex-transfer", "success")
			o.Order.CardID = card.ID
			if state == "unknown" {
				o.Order.Status = "submission_unknown"
				o.Order.Remote.Status = ""
			}
			if err := src.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			requested := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
			if state == "waiting" || state == "matched" {
				if _, err := src.db.Exec(`INSERT INTO pro_card_order_releases VALUES(?,?)`, o.Order.ID, formatTime(requested)); err != nil {
					t.Fatal(err)
				}
			} else if state != "opened" && state != "unknown" {
				if err := src.SetNexovccCase(o.Order.ID, state, "已核实实际付款回款情况", 0); err != nil {
					t.Fatal(err)
				}
			}
			if state == "matched" {
				f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{
					{ID: "paid", Serial: card.Serial, Authorization: "a", Currency: "USD", Amount: "10.00", MerchantCurrency: "USD", MerchantAmount: "10.00", Status: "PENDING", At: o.Order.CreatedAt.Add(time.Second)},
					{ID: "refund", Serial: card.Serial, Authorization: "a", Currency: "USD", Amount: "10.00", MerchantCurrency: "USD", MerchantAmount: "10.00", Status: "REVERSED", At: o.Order.CreatedAt.Add(2 * time.Second)},
				}}
				if err := src.SaveNexovccFinance(f); err != nil {
					t.Fatal(err)
				}
			}
			file, err := src.ExportProAccounts([]string{email}, model.ProDownstreamRef{})
			if err != nil {
				t.Fatal(err)
			}
			file = transferWire(t, file)
			if err = ValidateProTransfer(file); err != nil {
				t.Fatal("cannot import own export", err)
			}
			exported := file.Accounts[0].Orders[0]
			if exported.CardType != "nexovcc" || exported.ReturnConfirmed != (state == "matched") {
				t.Fatal(exported)
			}
			dst := transferStore(t)
			if _, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, false, model.ProDownstreamRef{}); err != nil {
				t.Fatal(err)
			}
			imported := nexStoreCard(t, dst)
			local, secret, err := dst.GPTPayCard(imported.ID)
			if err != nil {
				t.Fatal(err)
			}
			limit := 5
			local.MaxAccounts = &limit
			local, err = dst.SaveGPTPayCard(local, secret)
			if err != nil {
				t.Fatal(err)
			}
			released := state == "returned" || state == "unopened" || state == "matched"
			if local.Available != released {
				t.Fatal("wrong imported occupancy", state, local)
			}
			cases, err := dst.NexovccCases()
			if err != nil || len(cases) != 1 {
				t.Fatal(cases, err)
			}
			want := state
			if state == "auto" {
				want = "opened"
			}
			if state == "matched" {
				want = "returned"
			}
			if state == "unknown" {
				want = "unopened"
			}
			if cases[0].State != want || cases[0].Match != nil {
				t.Fatal("lost state or fabricated monetary history", cases)
			}
			if state == "waiting" || state == "matched" {
				if cases[0].RequestedAt != formatTime(requested) {
					t.Fatal("request time reset", cases)
				}
			}
		})
	}
}
