package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccOnlineLimitFinalAdmission(t *testing.T) {
	for _, status := range []string{"1", "2", "3", "4", "unknown"} {
		t.Run(status, func(t *testing.T) {
			s := transferStore(t)
			cfg := nexovcc.Defaults()
			cfg.MaxOnlineCards = 1
			if _, err := s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveNexovccCard(nexovcc.Card{Serial: "counted", Status: status}, nil); err != nil {
				t.Fatal(err)
			}
			err := s.BeginNexovccOperation(nexovcc.Operation{ID: "nex-admission1", Action: "open", Status: "submitted", CreatedAt: time.Now()})
			if (err == nil) != (status == "4") {
				t.Fatal("unexpected slot admission", status, err)
			}
		})
	}
}

func TestNexovccCapacityRecycleIgnoresManualRefundOverride(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	email := "pending-refund@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "pending-refund", "success")
	o.Order.CardID = c.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNexovccCase(o.Order.ID, "returned", "manual override", 0); err != nil {
		t.Fatal(err)
	}
	op := nexovcc.Operation{ID: "nex-autorecycle1", Action: "withdraw_cancel", CardID: c.ID, Serial: c.Serial, Status: "submitted", CapacityRecycle: true}
	if err := s.BeginNexovccOperation(op); err == nil {
		t.Fatal("manual state permitted automatic deletion without refund proof")
	}
}
