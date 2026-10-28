package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

func TestNexovccAutoRecycleThresholdKeepsUnusedAndStopsBelowLimit(t *testing.T) {
	s, f := nexFixtureServer(t)
	statuses := map[string]string{}
	balances := map[string]int64{}
	pans := []string{"5555555555554444", "4242424242424242", "4000000000000002"}
	var cards []nexovcc.Card
	finance := nexovcc.Finance{At: time.Now()}
	for i := range pans {
		serial := fmt.Sprintf("threshold-%d", i)
		card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: "1", Balance: "10.00", CreatedAt: time.Now().Add(time.Duration(i-3) * time.Hour)}, &nexovcc.PrivateCard{Number: pans[i], CVV: "123", Month: 12, Year: 2099})
		if err != nil {
			t.Fatal(err)
		}
		cards = append(cards, card)
		statuses[serial], balances[serial] = "1", 1000
		if i < 2 {
			for j, status := range []string{"PENDING", "REVERSED"} {
				finance.Trades = append(finance.Trades, nexovcc.Trade{ID: fmt.Sprintf("trade-%d-%d", i, j), Serial: serial, Authorization: "auth", Currency: "USD", Amount: "9.00", MerchantAmount: "9.00", MerchantCurrency: "USD", Status: status, At: time.Now().Add(time.Duration(j-2) * time.Minute)})
			}
		}
	}
	if err := s.store.SaveNexovccFinance(finance); err != nil {
		t.Fatal(err)
	}
	groups := nexovcc.Groups(finance)
	if len(groups) != 2 || !groups[0].FullyReturned || !groups[1].FullyReturned {
		t.Fatal("fixture must contain actual refunds", groups)
	}
	var calls []string
	var times []time.Time
	var funds []any
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		serial, _ := r.Body["serial_no"].(string)
		switch r.Path {
		case "/api/card/getUserCardDetail":
			for i, card := range cards {
				if card.Serial == serial {
					return nexReply(map[string]any{"serial_no": serial, "status": statuses[serial], "card_no": pans[i], "cvv": "123", "expire_at_str": "12/99"}), nil
				}
			}
		case "/api/card/getUserCardBalance":
			return nexReply(map[string]any{"balance": nexovcc.Money(balances[serial])}), nil
		case "/api/findLogWebFunds":
			filtered := []any{}
			for _, item := range funds {
				fund := item.(map[string]any)
				for i, card := range cards {
					if card.Serial == fund["serial_no"] && (r.Body["card_no"] == nil || r.Body["card_no"] == pans[i]) {
						filtered = append(filtered, item)
					}
				}
			}
			return nexReply(map[string]any{"list": filtered, "total": len(filtered), "page": 1}), nil
		case "/api/findLogCardTrades":
			var trades []any
			for _, tr := range finance.Trades {
				matches := r.Body["card_no"] == nil
				for i, card := range cards {
					if card.Serial == tr.Serial && r.Body["card_no"] == pans[i] {
						matches = true
					}
				}
				if !matches {
					continue
				}
				trades = append(trades, map[string]any{"log_no": tr.ID, "serial_no": tr.Serial, "business_token": tr.Authorization, "money_id": 3, "trade_amount": tr.Amount, "merchant_amount": tr.MerchantAmount, "merchant_currency": tr.MerchantCurrency, "status": tr.Status, "created_at": tr.At.Format(time.RFC3339Nano)})
			}
			return nexReply(map[string]any{"list": trades, "total": len(trades), "page": 1}), nil
		case "/api/card/userCardWithdraw", "/api/card/lockUserCard", "/api/card/deleteUserCard", "/api/card/createUserCard":
			if r.Path == "/api/card/createUserCard" || serial == cards[2].Serial {
				t.Fatal("proactive recycling must not open or retire a fresh unused card", r.Path, serial)
			}
			calls = append(calls, r.Path+":"+serial)
			times = append(times, time.Now())
			kind, amount := 0, ""
			switch r.Path {
			case "/api/card/userCardWithdraw":
				if r.Body["amount"] != "9.00" {
					t.Fatal(r.Body)
				}
				balances[serial], kind, amount = 100, 4002, "9.00"
			case "/api/card/lockUserCard":
				statuses[serial] = "2"
			case "/api/card/deleteUserCard":
				if statuses[serial] != "2" {
					t.Fatal("must freeze before deleting")
				}
				statuses[serial], kind, amount = "4", 3002, "1.00"
			}
			if kind != 0 {
				funds = append(funds, map[string]any{"serial_no": serial, "money_id": 3, "change_type_id": kind, "amount": amount, "after_balance": "110.00", "created_at": time.Now().Format(time.RFC3339Nano)})
			}
			return nexReply(nil), nil
		}
		return base(ctx, r)
	}
	for _, threshold := range []int{0, 4, 3, 1, 1} {
		cfg, _, _ := s.store.NexovccSettings()
		cfg.AutoRecycleAt = threshold
		if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
			t.Fatal(err)
		}
		if err := s.autoRecycleNexovccLocked(context.Background()); err != nil {
			t.Fatal(threshold, err)
		}
		want := 0
		if threshold == 3 {
			want = 3
		}
		if threshold == 1 {
			want = 6
		}
		if len(calls) != want || f.opens.Load() != 0 {
			t.Fatal(threshold, calls, f.opens.Load())
		}
		if want >= 3 && calls[0] != "/api/card/userCardWithdraw:"+cards[0].Serial {
			t.Fatal("did not choose oldest card", calls)
		}
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < 3*time.Second {
			t.Fatal("missing API operation gap", times)
		}
	}
	ops, _ := s.store.NexovccOperations()
	if len(ops) != 2 {
		t.Fatal("repeated scans created duplicate operations", ops)
	}
	for _, op := range ops {
		if !op.CapacityRecycle || op.Status != "succeeded" || op.WithdrawEvidence == "" || op.Evidence == "" {
			t.Fatal(op)
		}
	}
}

func TestNexovccRecycleRequiresEveryRefund(t *testing.T) {
	card := nexovcc.Card{ID: "card", Serial: "serial", Status: "1", Imported: true}
	good := nexovcc.TradeGroup{Serial: card.Serial, Valid: true, FullyReturned: true, Payment: "92.33"}
	for _, mode := range []string{"returned", "unused", "pending trade", "settled", "unverified", "unmatched account", "manual returned", "reserved", "manual waiting"} {
		t.Run(mode, func(t *testing.T) {
			g := good
			cases := []store.NexovccCase{{CardID: card.ID, PaymentStatus: "success", State: "returned", Match: &store.NexovccMatch{Group: g}}}
			groups := []nexovcc.TradeGroup{g}
			switch mode {
			case "unused":
				cases = nil
				groups = nil
			case "pending trade":
				groups[0].FullyReturned = false
			case "settled":
				groups[0].FullyReturned = false
				groups[0].Settled = true
			case "unverified":
				groups[0].Valid = false
			case "unmatched account":
				cases[0].Match = nil
			case "manual returned":
				cases[0].Match = nil
				cases[0].ManualState = "returned"
			case "reserved":
				cases[0].PaymentStatus = "submitted"
			case "manual waiting":
				cases[0].State = "waiting"
			}
			if got := nexovccRecycleCandidate(card, cases, groups); got != (mode == "returned" || mode == "unused") {
				t.Fatal(mode, got)
			}
		})
	}
}

func TestNexovccOnlineLimitRecyclesBeforeOpening(t *testing.T) {
	for _, entry := range []string{"manual", "automatic", "budget blocked", "refund pending"} {
		t.Run(entry, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			old, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "old-card", Status: "1", Balance: "10.00", CreatedAt: time.Now().Add(-time.Hour)}, &nexovcc.PrivateCard{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099})
			if err != nil {
				t.Fatal(err)
			}
			local, secret, _ := s.store.GPTPayCard(old.ID)
			local.Enabled = false
			if _, err = s.store.SaveGPTPayCard(local, secret); err != nil {
				t.Fatal(err)
			}
			cfg, _, _ := s.store.NexovccSettings()
			cfg.MaxOnlineCards = 1
			if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			p := model.DefaultProSettings()
			p.CardProvider = "nexovcc"
			p.NexovccAutoOpen = true
			p.NexovccTemplateID = "template"
			p.NexovccDailyOpenLimit = 5
			p.NexovccDailyAmountLimit = "100.00"
			if entry == "budget blocked" {
				p.NexovccDailyAmountLimit = "9.99"
			}
			if _, _, err = s.store.SaveProAutomationSettings(p, gptpay.Settings{}, ""); err != nil {
				t.Fatal(err)
			}
			status, balance := "1", int64(1000)
			var calls []string
			var times []time.Time
			var funds []any
			base := s.nexovcc.transport
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				switch r.Path {
				case "/api/card/findUserCardList":
					return nexReply(map[string]any{"list": []any{map[string]any{"serial_no": old.Serial, "status": status}}}), nil
				case "/api/card/getUserCardDetail":
					if r.Body["serial_no"] == old.Serial {
						return nexReply(map[string]any{"serial_no": old.Serial, "status": status, "card_no": "5555555555554444", "cvv": "123", "expire_at_str": "12/99"}), nil
					}
				case "/api/card/getUserCardBalance":
					if r.Body["serial_no"] == old.Serial {
						return nexReply(map[string]any{"balance": nexovcc.Money(balance)}), nil
					}
				case "/api/findLogCardTrades":
					if entry == "refund pending" {
						return nexReply(map[string]any{"list": []any{map[string]any{"log_no": "pending", "serial_no": old.Serial, "business_token": "auth", "money_id": 3, "trade_amount": "9.00", "merchant_amount": "9.00", "merchant_currency": "USD", "status": "PENDING", "created_at": time.Now().Format(time.RFC3339Nano)}}, "total": 1, "page": 1}), nil
					}
				case "/api/findLogWebFunds":
					return nexReply(map[string]any{"list": funds, "total": len(funds), "page": 1}), nil
				case "/api/card/userCardWithdraw", "/api/card/lockUserCard", "/api/card/deleteUserCard", "/api/card/createUserCard":
					calls = append(calls, r.Path)
					times = append(times, time.Now())
					if entry == "budget blocked" || entry == "refund pending" {
						t.Fatal("must not mutate before budget/refund eligibility", r.Path)
					}
					if r.Path == "/api/card/createUserCard" {
						if status != "4" || len(funds) != 2 {
							t.Fatal("opened before deletion/credit", status, funds)
						}
						return base(ctx, r)
					}
					if r.Body["serial_no"] != old.Serial {
						t.Fatal("wrong card recycled")
					}
					kind, amount := 0, ""
					if r.Path == "/api/card/userCardWithdraw" {
						if r.Body["amount"] != "9.00" {
							t.Fatal(r.Body)
						}
						balance = 100
						kind = 4002
						amount = "9.00"
					}
					if r.Path == "/api/card/lockUserCard" {
						status = "2"
					}
					if r.Path == "/api/card/deleteUserCard" {
						if status != "2" {
							t.Fatal("not frozen")
						}
						status = "4"
						kind = 3002
						amount = "1.00"
					}
					if kind != 0 {
						funds = append(funds, map[string]any{"serial_no": old.Serial, "money_id": 3, "change_type_id": kind, "amount": amount, "after_balance": "110.00", "created_at": time.Now().Format(time.RFC3339Nano)})
					}
					return nexReply(nil), nil
				}
				return base(ctx, r)
			}
			if entry == "manual" || entry == "refund pending" {
				_, err = s.openNexovccLocked(context.Background(), "nex-capacity-open1", "template", false)
			} else {
				_, err = s.ensureNexovccCard(context.Background())
			}
			if entry == "budget blocked" || entry == "refund pending" {
				if err == nil || len(calls) != 0 || f.opens.Load() != 0 {
					t.Fatal(err, calls, f.opens.Load())
				}
				if entry == "refund pending" {
					var waiting *nexovccCapacityWaitError
					if !errors.As(err, &waiting) {
						t.Fatal(err)
					}
				}
				return
			}
			if err != nil || f.opens.Load() != 1 || len(calls) != 4 {
				t.Fatal(err, calls, f.opens.Load())
			}
			for i := 1; i < len(times); i++ {
				if times[i].Sub(times[i-1]) < 3*time.Second {
					t.Fatal("missing delay", times)
				}
			}
			ops, _ := s.store.NexovccOperations()
			for _, op := range ops {
				if op.CapacityRecycle && (op.Status != "succeeded" || op.WithdrawEvidence == "" || op.Evidence == "") {
					t.Fatal(op)
				}
			}
		})
	}
}

func TestNexovccCapacityRestockWaitPreservesOriginalTask(t *testing.T) {
	s, f, email, c := restockWindowIntegration(t, func(http.ResponseWriter, *http.Request) { t.Error("payment must wait for a refundable card") })
	p, _, _, _ := s.store.ProSettings()
	p.CardProvider = "nexovcc"
	p.NexovccAutoOpen = true
	if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	c.NoRestockEnabled = false
	if _, err := s.store.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	old, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "occupied-card", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	local, secret, _ := s.store.GPTPayCard(old.ID)
	local.Enabled = false
	if _, err = s.store.SaveGPTPayCard(local, secret); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := s.store.NexovccSettings()
	cfg.MaxOnlineCards = 1
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		switch r.Path {
		case "/api/card/findUserCardList":
			return nexReply(map[string]any{"list": []any{map[string]any{"serial_no": old.Serial, "status": "1"}}}), nil
		case "/api/findLogCardTrades":
			return nexReply(map[string]any{"list": []any{map[string]any{"log_no": "payment", "serial_no": old.Serial, "business_token": "auth", "money_id": 3, "trade_amount": "9.00", "merchant_amount": "9.00", "merchant_currency": "USD", "status": "PENDING", "created_at": time.Now().Format(time.RFC3339Nano)}}, "total": 1, "page": 1}), nil
		case "/api/card/userCardWithdraw", "/api/card/lockUserCard", "/api/card/deleteUserCard", "/api/card/createUserCard":
			t.Fatal("capacity wait mutated supplier", r.Path)
		}
		return base(ctx, r)
	}
	windowTick(t, s)
	waitRestockPreparationIdle(t, s)
	tasks, err := s.store.ProRestockTasks()
	if err != nil || len(tasks) != 1 || tasks[0].Status != "reserved" || tasks[0].Email != email || tasks[0].SubmittedAt != nil {
		t.Fatal(tasks, err)
	}
	id := tasks[0].ID
	windowTick(t, s)
	waitRestockPreparationIdle(t, s)
	tasks, err = s.store.ProRestockTasks()
	if err != nil || len(tasks) != 1 || tasks[0].ID != id || tasks[0].Status != "reserved" || tasks[0].Email != email || f.opens.Load() != 0 {
		t.Fatal(tasks, err)
	}
}
