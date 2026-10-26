package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Stop at deterministic boundaries, including after the supplier has received
// the request. These exercise the real worker cleanup and card admission rules.
func TestProAutoStopPaymentSubmission(t *testing.T) {
	for _, boundary := range []string{"before_reserve", "after_reserve", "before_submit", "after_supplier_received"} {
		t.Run(boundary, func(t *testing.T) {
			reached, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var requests atomic.Int32
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if boundary == "after_supplier_received" {
					close(reached)
					<-release
				}
				w.WriteHeader(503)
				fmt.Fprint(w, `{"code":50301}`)
			})
			t.Cleanup(unblock)
			configureProAutoFixture(t, s)
			s.proAutoHooks = &proAutomationHooks{Session: func(ctx context.Context, email string, rpc func(string, map[string]any) (any, error)) error {
				_, err := rpc("pro_recharge", map[string]any{"session": map[string]any{
					"access_token": proAutoToken(email, "before"), "accessToken": proAutoToken(email, "before"),
					"session_token": "test-session", "user": map[string]any{"email": email}, "account": map[string]any{"id": "personal-id"},
				}})
				return err
			}}
			pause := func() { close(reached); <-release }
			if boundary == "before_reserve" {
				s.proAutoHooks.BeforePaymentReserve = pause
			} else if boundary == "after_reserve" {
				s.proAutoHooks.PaymentReserved = pause
			} else if boundary == "before_submit" {
				s.proAutoHooks.BeforePaymentSubmit = pause
			}
			rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"card_id": "test-card", "plan_code": "pro20"})
			if rec.Code != 202 {
				t.Fatal(rec.Body.String())
			}
			select {
			case <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not reach payment boundary")
			}
			rec = payCall(t, s.stopProAutomation, "email", email, nil)
			if rec.Code != 202 {
				t.Fatal(rec.Body.String())
			}
			unblock()
			p := waitProAutoFinished(t, s, email)
			if !p.ProAuto.StopRequested || p.ProAuto.Status != "stopped" {
				t.Fatal("payment completion undid stop", p.ProAuto)
			}
			orders, _, err := st.GPTPayOrders(10, 0, email)
			if err != nil {
				t.Fatal(err)
			}
			card, _, err := st.GPTPayCard("test-card")
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "after_supplier_received" {
				if requests.Load() != 1 || len(orders) != 1 || orders[0].Status != "submission_unknown" || card.PendingAccounts != 1 {
					t.Fatalf("submitted payment lost its protection: requests=%d orders=%+v pending=%d", requests.Load(), orders, card.PendingAccounts)
				}
				return
			}
			if requests.Load() != 0 || card.PendingAccounts != 0 {
				t.Fatalf("stopped before submission still paid/held card: requests=%d pending=%d", requests.Load(), card.PendingAccounts)
			}
			if boundary == "before_reserve" && len(orders) != 0 {
				t.Fatal("stop before reservation created an order")
			}
			if boundary != "before_reserve" && (len(orders) != 1 || orders[0].Status != "failed" || !strings.Contains(orders[0].Error, "未向供应商")) {
				t.Fatal("unsubmitted order was not retained as failed", orders)
			}
			if err = st.ReserveProCard("replacement@example.com", card.ID, false); err != nil {
				t.Fatal("stopped payment still blocks replacement", err)
			}
		})
	}
}
