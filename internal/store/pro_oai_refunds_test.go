package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"testing"
	"time"
)

func TestProOAIReturnedConfirmsBothPlatformsAndManualCorrections(t *testing.T) {
	for _, platform := range []string{"ejh", "nexovcc"} {
		t.Run(platform, func(t *testing.T) {
			s := transferStore(t)
			email := "refund@example.com"
			transferSource(t, s, email)
			var cardID string
			if platform == "ejh" {
				cardID = ejhFixtureCard(t, s).ID
			} else {
				cardID = nexStoreCard(t, s).ID
			}
			order := transferOrder(email, "returned-oai", "success")
			order.Order.CardID = cardID
			if err := s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
				t.Fatal(err)
			}
			_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProAuto.OrderID = order.Order.ID
				completeOAIPrerequisite(p, "merge_v2")
			})
			if err != nil {
				t.Fatal(err)
			}
			s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				v := p.ProOAIConfirmations["merge_v2"]
				v.Status = "review"
				v.Answer = "无法查询。\n待核实"
				v.Replies = []model.ProOAIReply{{At: time.Now(), Answer: v.Answer, Status: "review"}}
			})
			if changed, err := s.SyncProOAIRefundConfirmations(); err != nil || len(changed) != 0 {
				t.Fatal("unreturned account confirmed", changed, err)
			}
			setState := func(state string, revision int) {
				t.Helper()
				if platform == "ejh" {
					_, err = s.SetEJHCaseState(order.Order.ID, caseStateInput(t, s, order.Order.ID, state))
				} else {
					err = s.SetNexovccCase(order.Order.ID, state, "核实真实回款", revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			setState("returned", 0)
			var beforeOrder string
			s.db.QueryRow("SELECT profile FROM gptpay_orders WHERE id=?", order.Order.ID).Scan(&beforeOrder)
			if changed, err := s.SyncProOAIRefundConfirmations(); err != nil || len(changed) != 1 {
				t.Fatal("returned not confirmed", changed, err)
			}
			p, _, _ := s.MailAccountCredential(email)
			v := p.ProOAIConfirmations["merge_v2"]
			if v.Status != "confirmed" || v.ConfirmationSource != "card_return" || v.ConfirmedAt == nil || v.NextAttemptAt != nil || len(v.Replies) != 1 || v.Answer != "无法查询。\n待核实" {
				t.Fatal("confirmation lost source/history", v)
			}
			if changed, err := s.SyncProOAIRefundConfirmations(); err != nil || len(changed) != 0 {
				t.Fatal("reconciliation not idempotent", changed, err)
			}
			var afterOrder string
			s.db.QueryRow("SELECT profile FROM gptpay_orders WHERE id=?", order.Order.ID).Scan(&afterOrder)
			if beforeOrder != afterOrder {
				t.Fatal("OAI mutated finances")
			}
			if err = s.SaveProOAISession(email, "merge_v2", v.Generation-1, map[string]any{"cookies": "late"}); err == nil {
				t.Fatal("late Help callback accepted after returned")
			}
			setState("waiting", 1)
			if _, err = s.SyncProOAIRefundConfirmations(); err != nil {
				t.Fatal(err)
			}
			p, _, _ = s.MailAccountCredential(email)
			v = p.ProOAIConfirmations["merge_v2"]
			if v.Status != "review" || v.ConfirmedAt != nil || v.ConfirmationSource != "" {
				t.Fatal("manual correction ignored", v)
			}
		})
	}
}

func TestProOAIReturnBindingAndActualGlobalRefund(t *testing.T) {
	s := transferStore(t)
	email := "orders@example.com"
	transferSource(t, s, email)
	card := nexStoreCard(t, s)
	old := transferOrder(email, "old-order", "success")
	old.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(old.Order, old.Snapshot); err != nil {
		t.Fatal(err)
	}
	s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProAuto.OrderID = old.Order.ID
		completeOAIPrerequisite(p, "merge_v2")
	})
	newer := transferOrder(email, "newer-order", "success")
	newer.Order.CardID = card.ID
	newer.Order.CreatedAt = time.Now().Add(time.Hour)
	if err := s.CreateGPTPayOrder(newer.Order, newer.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNexovccCase(newer.Order.ID, "returned", "新订单已经回款", 0); err != nil {
		t.Fatal(err)
	}
	s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.OrderID = newer.Order.ID })
	if changed, err := s.SyncProOAIRefundConfirmations(); err != nil || len(changed) != 0 {
		t.Fatal("newer refund confirmed old inquiry", changed, err)
	}
	f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{
		{ID: "pay", Serial: card.Serial, Authorization: "auth", Currency: "USD", Amount: "10.00", MerchantCurrency: "USD", MerchantAmount: "10.00", Status: "PENDING", At: old.Order.CreatedAt.Add(time.Second)},
		{ID: "back", Serial: card.Serial, Authorization: "auth", Currency: "USD", Amount: "10.00", MerchantCurrency: "USD", MerchantAmount: "10.00", Status: "REVERSED", At: old.Order.CreatedAt.Add(2 * time.Second)},
	}}
	if err := s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.SyncProOAIRefundConfirmations(); err != nil || len(changed) != 1 {
		t.Fatal("actual matched refund ignored", changed, err)
	}
	p, _, _ := s.MailAccountCredential(email)
	if v := p.ProOAIConfirmations["merge_v2"]; v.OrderID != old.Order.ID || v.ConfirmationSource != "card_return" {
		t.Fatal("wrong refund binding", v)
	}
}
