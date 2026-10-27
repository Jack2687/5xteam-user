package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestProDeadCardBanDurableAndUnknownPaymentRetained(t *testing.T) {
	for _, issuer := range []string{"ejh", "nexovcc"} {
		for _, status := range []string{"success", "submission_unknown"} {
			t.Run(issuer+status, func(t *testing.T) {
				s := transferStore(t)
				email := "dead@example.com"
				transferSource(t, s, email)
				cardID := ""
				if issuer == "ejh" {
					cardID = ejhFixtureCard(t, s).ID
				} else {
					cardID = nexStoreCard(t, s).ID
				}
				order := transferOrder(email, "dead-order", status)
				order.Order.CardID = cardID
				if err := s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
					t.Fatal(err)
				}
				_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					p.ProDead = &model.ProDeadState{At: time.Now(), Reason: "account_deactivated", Source: "test"}
					p.ProAuto.CardID = cardID
				})
				if err != nil {
					t.Fatal(err)
				}
				c, secret, err := s.GPTPayCard(cardID)
				if err != nil || !c.DeadAccount || c.Available {
					t.Fatal(c, err)
				}
				if status == "submission_unknown" && c.PendingAccounts == 0 {
					t.Fatal("unknown payment released")
				}
				if issuer == "ejh" {
					cancelErr := s.EJHBeginOperation(ejh.Operation{ID: "dead-cancel", CardID: cardID, OrderNo: "fixture-card", Method: "cancel", Status: "submitting"})
					if (cancelErr == nil) != (status == "success") {
						t.Fatal("incorrect dead card cancellation admission", status, cancelErr)
					}
				}
				if status == "success" && c.ActiveAccounts != 0 {
					t.Fatal("dead success still occupies card", c)
				}
				c.Enabled = true
				if _, err = s.SaveGPTPayCard(c, secret); err != nil {
					t.Fatal(err)
				}
				if err = s.ReserveProCard("another@example.com", cardID, false); err == nil {
					t.Fatal("dead card admitted")
				}
				if status == "success" {
					if err = s.DeleteProAccount(email); err != nil {
						t.Fatal(err)
					}
					if _, err = s.SaveMailAccountToPro(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email}); err != nil {
						t.Fatal(err)
					}
					p, _, err := s.MailAccountCredential(email)
					if err != nil || !p.IsProDead() {
						t.Fatal("reimport lost death", err)
					}
					if issuer == "ejh" {
						rows, e := s.EJHCases()
						if e != nil || len(rows) != 1 || rows[0].ProDead == nil {
							t.Fatal("case death lost", e)
						}
					} else {
						rows, e := s.NexovccCases()
						if e != nil || len(rows) != 1 || rows[0].ProDead == nil {
							t.Fatal("case death lost", e)
						}
					}
				}
			})
		}
	}
}

func TestProDeadPauseOnlyOnFirstDetection(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "continue", true: "pause"}[pause], func(t *testing.T) {
			s := transferStore(t)
			email := "dead-policy@example.com"
			transferSource(t, s, email)
			c := restock.DefaultConfig()
			c.Enabled = true
			c.Mode = "auto"
			c.GroupID = 1
			c.DeadAccountPause = pause
			c, err := s.SaveProRestockConfig(c)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProDead = &model.ProDeadState{At: time.Now(), Reason: "account_deactivated"}
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ProRestockConfig()
			if err != nil || got.Enabled == pause {
				t.Fatal(got, err)
			}
			got.Enabled = true
			if _, err = s.SaveProRestockConfig(got); err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProDead = nil; p.ProAuto.Status = "running" })
			if err != nil {
				t.Fatal(err)
			}
			got, _ = s.ProRestockConfig()
			p, _, _ := s.MailAccountCredential(email)
			if !got.Enabled || !p.IsProDead() || p.ProAuto.Status != "dead" {
				t.Fatal("stale update revived death or paused repeatedly")
			}
		})
	}
}

func TestProProxyRetryNeverReplaysPayment(t *testing.T) {
	for _, paid := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepayment", true: "paid"}[paid], func(t *testing.T) {
			s := transferStore(t)
			email := "proxy@example.com"
			transferSource(t, s, email)
			p, e := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProAuto = model.ProAutoState{ID: "proxy-flow", Status: "running", Stage: "login", CardID: "same-card", RetryAttempts: 2, Steps: map[string]string{"login": "running"}}
			})
			if e != nil {
				t.Fatal(e)
			}
			if paid {
				o := transferOrder(email, "paid-order", "success")
				if e = s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
					t.Fatal(e)
				}
			}
			initial := p.ProAuto
			for i := 0; i < 4; i++ {
				next, err := s.PrepareProAutoProxyRetry(email, p.ProAuto, 3, time.Now(), "proxy wait")
				if paid || i == 3 {
					if err == nil {
						t.Fatal("unsafe/unbounded proxy retry")
					}
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if next.ProAuto.RetryAttempts != 2 || next.ProAuto.ProxyRetryAttempts != i+1 || next.ProAuto.CardID != "same-card" || next.ProAuto.PaymentOrderID() != initial.PaymentOrderID() {
					t.Fatal("proxy retry changed purchase identity")
				}
				p = next
			}
			if !paid {
				if _, err := s.PrepareProAutoProxyRetry(email, initial, 3, time.Now(), "stale"); err == nil {
					t.Fatal("stale retry accepted")
				}
			}
		})
	}
}
