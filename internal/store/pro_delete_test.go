package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProDeleteKeepsPaymentHistoryAndCardUsage(t *testing.T) {
	s := transferStore(t)
	const email = "delete@example.com"
	transferSource(t, s, email)
	capacityCard(t, s, "card")
	order := transferOrder(email, "paid-delete", "success")
	if err := s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProOAuthSession(model.ProOAuthSession{ID: "old-authorization", TargetEmail: email, ExpiresAt: time.Now().Add(time.Hour)}, "verifier"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProAccount(email); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MailAccountCredential(email); err == nil {
		t.Fatal("credentials retained")
	}
	if _, _, err := s.ProOAuthSession("old-authorization"); err == nil {
		t.Fatal("deleted account can still be authorized by old callback")
	}
	if _, _, err := s.GPTPayOrder(order.Order.ID); err != nil {
		t.Fatal("payment history removed", err)
	}
	card, _, err := s.GPTPayCard("card")
	if err != nil || card.OpenedAccounts != 1 {
		t.Fatal("deleting account reset card usage", card.OpenedAccounts, err)
	}
}

func TestProDeleteBlocksActiveRestock(t *testing.T) {
	for _, status := range []string{"reserved", "submitting", "running", "verifying", "unknown", "attention", "ready", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			s := transferStore(t)
			const email = "restock-delete@example.com"
			transferSource(t, s, email)
			c := restock.DefaultConfig()
			c.Enabled, c.Mode, c.GroupID = true, "auto", 1
			c, err := s.SaveProRestockConfig(c)
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := s.ReserveProRestock(restock.Decision{ID: "delete-guard", At: time.Now(), Version: c.Version, Suggested: 1}, "target", 0)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("reserve: %v %v", tasks, err)
			}
			task := tasks[0]
			task.Status = status
			if err = s.SaveProRestockTask(task); err != nil {
				t.Fatal(err)
			}
			if task.Active() {
				if err = s.CheckProAccountDeletion(email); err == nil {
					t.Error("preflight allowed active restock deletion")
				}
				if err = s.DeleteMailAccount(email); err == nil {
					t.Fatal("mailbox deletion bypassed active restock guard")
				}
				if err = s.DeleteProAccount(email); err == nil {
					t.Fatal("deleted active restock account")
				}
			} else if err = s.DeleteProAccount(email); err != nil {
				t.Fatal(err)
			}
			if linked, err := s.ProRestockDecisionTasks(task.DecisionID); err != nil || len(linked) != 1 {
				t.Fatal("restock history lost", err)
			}
		})
	}
}

func TestProDeleteBlocksPendingPayment(t *testing.T) {
	for _, status := range []string{"submitting", "submission_unknown", "processing"} {
		t.Run(status, func(t *testing.T) {
			s := transferStore(t)
			const email = "pending@example.com"
			transferSource(t, s, email)
			order := transferOrder(email, "pending-delete", status)
			if err := s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteProAccount(email); err == nil {
				t.Fatal("deleted unresolved purchase")
			}
			if _, _, err := s.MailAccountCredential(email); err != nil {
				t.Fatal(err)
			}
		})
	}
}
