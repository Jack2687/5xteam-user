package httpapi

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccPairedSnapshotsContinueAfterFailure(t *testing.T) {
	for _, failure := range []string{"", "balance", "trades", "funds", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			s, _ := nexFixtureServer(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			old := time.Now().Add(-time.Hour)
			cards := map[string]nexovcc.Card{}
			numbers := map[string]string{"a": "4242424242424242", "b": "5555555555554444"}
			f := nexovcc.Finance{At: old, CardAt: map[string]time.Time{"a": old, "b": old.Add(time.Minute)}}
			for _, serial := range []string{"a", "b"} {
				card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: "1", Balance: "1.00", BalanceAt: old}, &nexovcc.PrivateCard{Number: numbers[serial], CVV: "123", Month: 12, Year: 2099})
				if err != nil {
					t.Fatal(err)
				}
				cards[serial] = card
				f.Funds = append(f.Funds, nexovcc.Fund{ID: serial + "-old", Serial: serial, Kind: 3001, Amount: "-10.00", Currency: "USD", At: old})
			}
			if err := s.store.SaveNexovccFinance(f); err != nil {
				t.Fatal(err)
			}
			base := s.nexovcc.transport
			var calls []string
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				kind := map[string]string{"/api/card/getUserCardBalance": "balance", "/api/findLogCardTrades": "trades", "/api/findLogWebFunds": "funds"}[r.Path]
				if kind == "" {
					return base(ctx, r)
				}
				serial := nexovcc.String(r.Body["serial_no"])
				if kind != "balance" {
					for k, n := range numbers {
						if r.Body["card_no"] == n {
							serial = k
						}
					}
				}
				calls = append(calls, serial+":"+kind)
				// Even after reading the new balance and trades, the dashboard must
				// keep this card's entire previous snapshot until funds complete.
				snapshot, finance, _, _, err := s.store.NexovccFinancialSnapshot()
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range snapshot {
					if c.Serial == serial && (c.Balance != "1.00" || !c.BalanceAt.Equal(old) || !finance.CardSyncedAt(serial).Equal(f.CardSyncedAt(serial))) {
						t.Fatal("published partial data", c, finance)
					}
					if serial == "b" && c.Serial == "a" && failure == "" && c.Balance != "10.00" {
						t.Fatal("first card not committed before next card")
					}
				}
				if serial == "a" && kind == failure {
					return nexovcc.Response{Status: 500}, nil
				}
				if serial == "a" && kind == "funds" && failure == "cancel" {
					cancel()
				}
				return base(ctx, r)
			}
			err := s.syncNexovccTransactionsLocked(ctx, "")
			if (err != nil) != (failure != "") {
				t.Fatal(err)
			}
			want := []string{"a:balance"}
			if failure != "balance" {
				want = append(want, "a:trades")
			}
			if failure != "balance" && failure != "trades" {
				want = append(want, "a:funds")
			}
			if failure != "cancel" {
				want = append(want, "b:balance", "b:trades", "b:funds")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("card reads interleaved or stopped early", calls, want)
			}
			for serial, original := range cards {
				got, _ := s.store.NexovccCard(original.ID)
				var finance nexovcc.Finance
				if err := s.store.NexovccCached("finance", &finance); err != nil {
					t.Fatal(err)
				}
				failed := failure == "cancel" || (serial == "a" && failure != "")
				if failed {
					if got.Balance != original.Balance || !got.BalanceAt.Equal(old) || !finance.CardSyncedAt(serial).Equal(f.CardSyncedAt(serial)) {
						t.Fatal("failed attempt overwrote snapshot", got, finance)
					}
					if failure != "cancel" && got.Error == "" {
						t.Fatal("missing card failure")
					}
				} else if got.Balance != "10.00" || !finance.CardSyncedAt(serial).After(old) || got.Error != "" {
					t.Fatal("complete snapshot missing", got, finance)
				}
			}
		})
	}
}

func TestNexovccWaitingReturnPriorityAndCardCheckpoint(t *testing.T) {
	for _, admissionBusy := range []bool{false, true} {
		t.Run(map[bool]string{false: "withdraw-before-next-card", true: "pro-admission-busy"}[admissionBusy], func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			cfg, _, _ := s.store.NexovccSettings()
			cfg.AutoWithdrawEnabled = true
			if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-2 * time.Hour)
			numbers := map[string]string{"ordinary": "5555555555554444", "waiting": "4242424242424242"}
			cards := map[string]nexovcc.Card{}
			for serial, number := range numbers {
				card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: "1", Balance: "1.00", BalanceAt: old}, &nexovcc.PrivateCard{Number: number, CVV: "123", Month: 12, Year: 2099})
				if err != nil {
					t.Fatal(err)
				}
				cards[serial] = card
			}
			order := gptpay.Order{ID: "waiting-order", Email: "waiting@example.com", CardID: cards["waiting"].ID, Status: "success", CreatedAt: old, SucceededAt: &old}
			order.Remote.PaymentAmount, order.Remote.PaymentCurrency = "9.00", "USD"
			if err := s.store.CreateGPTPayOrder(order, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: numbers["waiting"], CVV: "123", Month: 12, Year: 2099}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.store.SaveMailAccountToPro(model.MailAccountProfile{Email: order.Email}, model.MailAccountCredentials{Email: order.Email}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.store.UpdateProAccount(order.Email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
				t.Fatal(err)
			}
			if err := s.store.SaveNexovccFinance(nexovcc.Finance{At: old, CardAt: map[string]time.Time{"ordinary": old.Add(-time.Hour), "waiting": old}}); err != nil {
				t.Fatal(err)
			}
			base := s.nexovcc.transport
			var firstBalance string
			writes := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				switch r.Path {
				case "/api/card/getUserCardBalance":
					serial := nexovcc.String(r.Body["serial_no"])
					if firstBalance == "" {
						firstBalance = serial
					}
					if serial == "ordinary" && !admissionBusy && writes != 1 {
						t.Fatal("finished card waited for rest of batch")
					}
					balance := "10.00"
					if writes > 0 && serial == "waiting" {
						balance = "1.00"
					}
					return nexReply(map[string]any{"balance": balance}), nil
				case "/api/findLogCardTrades":
					if r.Body["card_no"] == numbers["waiting"] {
						rows := []any{}
						for i, status := range []string{"PENDING", "REVERSED"} {
							rows = append(rows, map[string]any{"log_no": status, "serial_no": "waiting", "business_token": "auth", "money_id": 3, "trade_amount": "9.00", "merchant_amount": "9.00", "merchant_currency": "USD", "status": status, "created_at": old.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)})
						}
						return nexReply(map[string]any{"list": rows, "total": 2, "page": r.Body["page"]}), nil
					}
				case "/api/card/userCardWithdraw":
					if admissionBusy || s.yieldNexovccRead() {
						t.Fatal("money operation exposed read-only cancellation")
					}
					if s.proCardAdmissionMu.TryLock() {
						s.proCardAdmissionMu.Unlock()
						t.Fatal("missing admission lock")
					}
					if s.nexovcc.mu.TryLock() {
						s.nexovcc.mu.Unlock()
						t.Fatal("missing issuer lock")
					}
					writes++
					return nexReply(nil), nil
				case "/api/findLogWebFunds":
					if writes > 0 && r.Body["card_no"] == numbers["waiting"] {
						return nexReply(map[string]any{"list": []any{map[string]any{"ID": "receipt", "serial_no": "waiting", "money_id": 3, "change_type_id": 4002, "amount": "9.00", "created_at": time.Now().Format(time.RFC3339Nano)}}, "total": 1, "page": r.Body["page"]}), nil
					}
				}
				if strings.Contains(r.Path, "deleteUserCard") {
					t.Fatal("unexpected closure")
				}
				return base(ctx, r)
			}
			s.nexovcc.mu.Lock()
			defer s.nexovcc.mu.Unlock()
			if admissionBusy {
				s.proCardAdmissionMu.Lock()
				defer s.proCardAdmissionMu.Unlock()
			}
			ctx, finish := s.nexovccReadContext(nexovccScheduledSync(context.Background()))
			defer finish()
			if err := s.syncNexovccTransactionsLocked(ctx, ""); err != nil {
				t.Fatal(err)
			}
			if firstBalance != "waiting" {
				t.Fatal("waiting return did not have priority", firstBalance)
			}
			if !s.yieldNexovccRead() {
				t.Fatal("read cancellation not restored")
			}
		})
	}
}
