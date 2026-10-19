package store

import (
	"testing"

	"chatgpt-space-merge/internal/ejh"
)

func TestEJHClosedCardExcludedAndAdmissionBlocked(t *testing.T) {
	for _, masked := range []bool{false, true} {
		name := "complete_response"
		if masked {
			name = "masked_response"
		}
		t.Run(name, func(t *testing.T) {
			s := transferStore(t)
			card := ejhFixtureCard(t, s)
			capacityCard(t, s, "alias")
			var record ejh.CardRecord
			if err := s.EJHGet("card", card.ID, &record); err != nil {
				t.Fatal(err)
			}
			record.Remote.Status = "2"
			if masked {
				record.Remote.Number = "****4242"
				record.Remote.CVV = ""
			}
			_, err := s.ImportEJHCard(record.Remote, false)
			if (err != nil) != masked {
				t.Fatalf("unexpected import result: %v", err)
			}
			for _, id := range []string{card.ID, "alias"} {
				got, _, err := s.GPTPayCard(id)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Enabled || got.Available || got.IssuerStatus != "2" {
					t.Fatalf("closed card must remain unavailable independently of local switch: %+v", got)
				}
				if err := s.ReserveProCard("fixture@example.com", id, false); err == nil {
					t.Fatal("closed card admitted for automation")
				}
				o := transferOrder("fixture@example.com", "closed-"+id, "submitting")
				o.Order.CardID = id
				if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err == nil {
					t.Fatal("closed card admitted for direct purchase")
				}
			}
			rows, total, err := s.GPTPayCards(20, 0, true)
			if err != nil || total != 0 || len(rows) != 0 {
				t.Fatalf("closed card leaked into selectable list: %d, %v", total, err)
			}
		})
	}
}
