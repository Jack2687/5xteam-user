package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/store"
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEJHClosedRefundRecovery(t *testing.T) {
	for _, source := range []string{"previously_closed", "cancel_action", "issuer_closed"} {
		t.Run(source, func(t *testing.T) {
			s, _ := ejhTestServer(t, 200)
			now := time.Now()
			remote := ejh.Card{OrderNo: "closure-order", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "3", CardCurrency: "PHP"}
			if source == "previously_closed" {
				remote.Status = "2"
			}
			card, err := s.store.ImportEJHCard(remote, false)
			if err != nil {
				t.Fatal(err)
			}
			var saved ejh.CardRecord
			if err = s.store.EJHGet("card", card.ID, &saved); err != nil {
				t.Fatal(err)
			}
			old := now.Add(-time.Hour)
			saved.Balance, saved.BalanceAt, saved.SyncedAt = "12971.04", &old, old
			if err = s.store.EJHPut("card", card.ID, saved); err != nil {
				t.Fatal(err)
			}
			refund := ejh.Trade{OrderNo: remote.OrderNo, TradeNo: "closure-refund", Type: "17", Status: "2", TradeAmount: "12971.04", TradeCurrency: "PHP", BillAmount: "207.70", BillCurrency: "USD", TradeTime: now.Format(time.RFC3339Nano)}
			zero := refund
			zero.TradeNo, zero.TradeAmount, zero.BillAmount = "second-refund", "0", "0"
			rows := []ejh.Trade{}
			tradeCalls, cancelCalls := 0, 0
			fail := false
			transport := ejhFixtureTransport(t, func(method string, data map[string]any) any {
				switch method {
				case "card.cancel.info":
					cancelCalls++
					return map[string]any{"orderNo": remote.OrderNo, "status": "SUCCESS"}
				case "card.query.info":
					if source != "cancel_action" || cancelCalls != 1 {
						t.Fatal("unexpected cancellation check")
					}
					closed := remote
					closed.Status = "2"
					return closed
				case "card.list.info":
					closed := remote
					closed.Status = "2"
					return map[string]any{"cardInfos": []ejh.Card{closed}}
				case "card.trade.info":
					tradeCalls++
					if data["orderNo"] != remote.OrderNo {
						t.Fatal("queried the wrong card")
					}
					if fail {
						return map[string]any{}
					}
					return map[string]any{"tradeInfos": rows}
				default:
					t.Fatalf("closed refund recovery must not query card details/balance or mutate funds: %s", method)
					return nil
				}
			})
			s.ejh.client.HTTP = transport
			if source == "cancel_action" {
				r := httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"cancel","confirm":true,"request_id":"closure-cancel-fixture"}`))
				r.SetPathValue("id", card.ID)
				w := httptest.NewRecorder()
				s.ejhCardAction(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) || cancelCalls != 1 {
					t.Fatal("cancel failed", w.Code, w.Body.String(), cancelCalls)
				}
			}
			sync := func() error {
				return s.syncEJHLocked(context.Background(), now.Add(-2*time.Hour), now.Add(time.Minute))
			}
			// Even an empty first result must leave the card eligible next round.
			if err = sync(); err != nil || tradeCalls != 1 {
				t.Fatal("closed card was not backfilled", err, tradeCalls)
			}
			if err = s.store.EJHGet("card", card.ID, &saved); err != nil || saved.Remote.Status != "2" {
				t.Fatal("card is not closed", err)
			}
			for _, stage := range []string{"account_refund", "pending", "unknown", "missing_transaction", "invalid_transaction", "missing_currency", "missing_settlement", "invalid_settlement", "invalid_bill_currency", "another_pending"} {
				t.Run(stage, func(t *testing.T) {
					v := refund
					switch stage {
					case "account_refund":
						v.Type = "5"
					case "pending":
						v.Status = "1"
					case "unknown":
						v.Status = "unknown"
					case "missing_transaction":
						v.TradeAmount = ""
					case "invalid_transaction":
						v.TradeAmount = "-1"
					case "missing_currency":
						v.TradeCurrency = ""
					case "missing_settlement":
						v.BillAmount = ""
					case "invalid_settlement":
						v.BillAmount = "invalid"
					case "invalid_bill_currency":
						v.BillCurrency = "???"
					}
					rows = []ejh.Trade{v}
					if stage == "another_pending" {
						pending := zero
						pending.Status = "1"
						rows = append(rows, pending)
					}
					before := tradeCalls
					// Repeat the same result: incomplete records must not stop polling.
					for range 2 {
						if err = sync(); err != nil {
							t.Fatal(err)
						}
					}
					if tradeCalls != before+2 {
						t.Fatal("stopped before all refund amounts were confirmed", tradeCalls, before)
					}
				})
			}
			beforeFailure, err := store.EJHRecords[ejh.TradeRecord](s.store, "trade")
			if err != nil {
				t.Fatal(err)
			}
			fail = true
			if err = sync(); err == nil {
				t.Fatal("missing collection failure")
			}
			afterFailure, err := store.EJHRecords[ejh.TradeRecord](s.store, "trade")
			if err != nil || !reflect.DeepEqual(beforeFailure, afterFailure) {
				t.Fatal("failed query changed saved refunds", err)
			}
			fail, rows = false, []ejh.Trade{refund, zero}
			if err = sync(); err != nil {
				t.Fatal(err)
			}
			// A new server instance must honor persisted completion, including a
			// confirmed zero refund, without an in-memory completion flag.
			s = &Server{store: s.store}
			s.ejh.client = ejh.New()
			s.ejh.client.HTTP = transport
			before := tradeCalls
			for range 2 {
				if err = sync(); err != nil {
					t.Fatal(err)
				}
			}
			if tradeCalls != before {
				t.Fatal("settled closed card still queried", tradeCalls, before)
			}
			var after ejh.CardRecord
			if err = s.store.EJHGet("card", card.ID, &after); err != nil || !reflect.DeepEqual(saved, after) {
				t.Fatal("refund polling changed the closed card snapshot", err)
			}
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"start":"`+now.AddDate(0, 0, -1).Format("2006-01-02")+`","end":"`+now.AddDate(0, 0, 1).Format("2006-01-02")+`","page":1}`))
			r.SetPathValue("id", card.ID)
			w := httptest.NewRecorder()
			s.queryEJHCardTrades(w, r)
			var reply struct {
				Data struct {
					Items     []ejh.Trade `json:"items"`
					Cached    bool        `json:"cached"`
					QueriedAt time.Time   `json:"queried_at"`
				} `json:"data"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &reply); err != nil || w.Code != 200 || !reply.Data.Cached || !reply.Data.QueriedAt.After(saved.SyncedAt) || tradeCalls != before {
				t.Fatal("cached history did not reflect refund collection", w.Body.String(), err)
			}
			found := false
			for _, v := range reply.Data.Items {
				if v.Type == "17" && v.TradeNo == refund.TradeNo && v.TradeAmount == refund.TradeAmount && v.BillAmount == refund.BillAmount && v.Status == "2" {
					found = true
				}
			}
			if !found {
				t.Fatal("confirmed refund is missing from card history")
			}
		})
	}
}
