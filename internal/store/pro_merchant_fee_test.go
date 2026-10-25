package store

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"testing"
	"time"
)

func TestProMerchantFeeFinalSuccessExactlyOnce(t *testing.T) {
	for _, issuer := range []string{"ejh", "nexovcc"} {
		t.Run(issuer, func(t *testing.T) {
			s := transferStore(t)
			email := "fee@example.com"
			transferSource(t, s, email)
			var id string
			if issuer == "ejh" {
				id = ejhFixtureCard(t, s).ID
			} else {
				id = nexStoreCard(t, s).ID
			}
			o := transferOrder(email, "fee-order", "success")
			o.Order.CardID = id
			if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			cfg := model.DefaultProSettings()
			cfg.AutoMerchantEnabled = true
			cfg.MerchantSuccessFeeUSD = "1.25"
			if _, _, err := s.SaveProAutomationSettings(cfg, gptpay.Settings{PlanCode: "pro5"}, "", "fixture-key"); err != nil {
				t.Fatal(err)
			}
			p, _, err := s.PrepareProMerchant(email)
			if err != nil || p.ProMerchant.SuccessFeeUSD != "1.25" || p.ProMerchant.FeeCardID != id || p.ProMerchant.FeeIssuer != issuer {
				t.Fatal(p.ProMerchant, err)
			}
			for _, state := range []string{"waiting", "failed"} {
				_, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					now := time.Now()
					p.ProMerchant.AcceptedAt = &now
					p.ProMerchant.Status = state
				})
				if err != nil {
					t.Fatal(err)
				}
				fees, err := s.ProMerchantFees(issuer)
				if err != nil || len(fees) != 0 {
					t.Fatal("charged before success", fees, err)
				}
			}
			cfg.MerchantSuccessFeeUSD = "9.99"
			if _, _, err = s.SaveProAutomationSettings(cfg, gptpay.Settings{PlanCode: "pro5"}, "", ""); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				_, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					now := time.Now()
					p.ProMerchant.Status = "completed"
					p.ProMerchant.CompletedAt = &now
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			fees, err := s.ProMerchantFees(issuer)
			if err != nil || len(fees) != 1 || fees[0].Amount != "1.25" || fees[0].OrderID != "fee-order" {
				t.Fatal("wrong receipt", fees, err)
			}
			if issuer == "ejh" {
				cases, err := s.EJHCases()
				if err != nil || len(cases) != 1 || cases[0].MerchantFeeUSD != "1.25" {
					t.Fatal(cases, err)
				}
			} else {
				cases, err := s.NexovccCases()
				if err != nil || len(cases) != 1 || cases[0].MerchantFeeUSD != "1.25" {
					t.Fatal(cases, err)
				}
			}
			if _, err = s.db.Exec("DELETE FROM mail_accounts WHERE email=?", email); err != nil {
				t.Fatal(err)
			}
			fees, err = s.ProMerchantFees(issuer)
			if err != nil || len(fees) != 1 {
				t.Fatal("receipt lost after account removal", err)
			}
		})
	}
	for _, bad := range []string{"-1", "0.001", "1000000.01", "abc"} {
		if _, err := merchantFeeAmount(bad); err == nil {
			t.Fatal("invalid fee accepted", bad)
		}
	}
}
