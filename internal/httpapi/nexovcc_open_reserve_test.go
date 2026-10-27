package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccAutomaticOpenAfterDefiniteFailure(t *testing.T) {
	for _, status := range []string{"failed", "unknown", "import_pending"} {
		t.Run(status, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			p, _, _, _ := s.store.ProSettings()
			p.CardProvider, p.NexovccAutoOpen, p.NexovccTemplateID = "nexovcc", true, "template"
			p.NexovccDailyOpenLimit, p.NexovccDailyAmountLimit = 10, "100.00"
			if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
				t.Fatal(err)
			}
			op := nexovcc.Operation{ID: "nex-oldfailed", Action: "open", Status: "submitted", Automatic: true, Amount: "10.00", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := s.store.BeginNexovccOperation(op); err != nil {
				t.Fatal(err)
			}
			op.Status = status
			if err := s.store.SaveNexovccOperation(op); err != nil {
				t.Fatal(err)
			}
			card, err := s.ensureNexovccCard(context.Background())
			if status == "failed" {
				if err != nil || card.ID == "" || f.opens.Load() != 1 {
					t.Fatal(card, err, f.opens.Load())
				}
			} else if err == nil || f.opens.Load() != 0 {
				t.Fatal("uncertain issuance replayed", err, f.opens.Load())
			}
		})
	}
}

func TestNexovccOpenIncludesAccountReserve(t *testing.T) {
	for _, balance := range []string{"10.99", "11.00", "167.51"} {
		t.Run(balance, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			if balance == "167.51" {
				cfg, key, _ := s.store.NexovccSettings()
				cfg.Templates[0].Amount, cfg.Templates[0].FeeBuffer = "150", "20"
				if _, err := s.store.SaveNexovccSettings(cfg, key, false); err != nil {
					t.Fatal(err)
				}
			}
			base := s.nexovcc.transport
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/getBalanceInfo" {
					return nexReply([]any{map[string]any{"money_id": 3, "balance": balance, "freeze_balance": "0.00"}}), nil
				}
				return base(ctx, r)
			}
			op, err := s.openNexovccLocked(context.Background(), "nex-reservecheck1", "template", false)
			if balance != "11.00" {
				if !errors.Is(err, errIssuerInsufficientBalance) || f.opens.Load() != 0 {
					t.Fatal("reserve ignored", op, err)
				}
			} else if err != nil || op.Status != "succeeded" || f.opens.Load() != 1 {
				t.Fatal(op, err)
			}
		})
	}
}
