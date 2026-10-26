package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
)

func TestGPTPayUnsubmittedCleanupGuards(t *testing.T) {
	for _, change := range []string{"none", "status", "response", "request_id", "updated", "identity", "initial_response", "initial_unknown"} {
		t.Run(change, func(t *testing.T) {
			s := transferStore(t)
			capacityCard(t, s, "card")
			o := transferOrder("stopped@example.com", "local-order", "submitting")
			o.Order.CardID, o.Order.Remote = "card", gptpay.RemoteOrder{}
			if change == "initial_response" {
				o.Order.Remote.FailureReason = "supplier response"
			}
			if change == "initial_unknown" {
				o.Order.Status = "submission_unknown"
			}
			if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			expected, current := o.Order, o.Order
			switch change {
			case "status":
				current.Status = "processing"
			case "response":
				current.Remote.ID = "received"
			case "request_id":
				current.RequestID = "supplier-request"
			case "updated":
				current.UpdatedAt = time.Now()
			case "identity":
				expected.Email = "another@example.com"
			}
			if change == "status" || change == "response" || change == "request_id" || change == "updated" {
				if err := s.UpdateGPTPayOrder(current); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.FailUnsubmittedGPTPayOrder(expected)
			if (err == nil) != (change == "none") {
				t.Fatalf("cleanup guard %s: %v", change, err)
			}
			saved, _, err := s.GPTPayOrder(o.Order.ID)
			if err != nil {
				t.Fatal(err)
			}
			var pending int
			if err = s.db.QueryRow("SELECT count(*) FROM pro_card_usage WHERE order_id=? AND status NOT IN ('failed','success')", o.Order.ID).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if change == "none" {
				if saved.Status != "failed" || saved.Active() || pending != 0 {
					t.Fatal("cleanup left payment occupancy", saved, pending)
				}
			} else if saved.Status != current.Status || !saved.Active() || pending != 1 {
				t.Fatal("rejected cleanup changed order/usage", saved, pending)
			}
		})
	}
}
