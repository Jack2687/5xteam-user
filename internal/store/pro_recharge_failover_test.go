package store

import (
	"chatgpt-space-merge/internal/restock"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

func setupRechargeFailover(t *testing.T, s *Store, root string, limit, backupLimit int) {
	t.Helper()
	backup := gptpay.Settings{Provider: gptpay.CMSNav, Country: "US", PlanCode: "pro20", TermsAccepted: true}
	if _, err := s.SaveGPTPaySettings(backup, "backup-key"); err != nil {
		t.Fatal(err)
	}
	pay, err := s.SaveGPTPaySettings(gptpay.Settings{Provider: gptpay.Tokenseek, BackupProvider: gptpay.CMSNav, Country: "JP", PlanCode: "pro5", CardAccountLimit: 7}, "primary-key")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateProAccount(root, func(p *model.MailAccountProfile) {
		p.ProAuto.ID = "root-" + root
		p.ProAuto.Replacement = &model.ProRechargeReplacement{RootEmail: root, Limit: limit, FailoverLimit: backupLimit, PrimaryProvider: pay.Provider, BackupProvider: pay.BackupProvider, Payment: model.RechargePayment(pay)}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRechargeFailoverBoundedAndRestart(t *testing.T) {
	for _, limit := range []int{0, 1, 10} {
		for _, backupLimit := range []int{0, 1, 10} {
			t.Run(fmt.Sprintf("%d/%d", limit, backupLimit), func(t *testing.T) {
				dir := t.TempDir()
				s, err := Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { s.Close() }()
				root := replacementAccounts(t, s, 23)
				setupRechargeFailover(t, s, root, limit, backupLimit)
				current := root
				for n := 1; n <= limit+backupLimit; n++ {
					failReplacement(t, s, current, 10)
					next, e := s.ClaimProRechargeReplacement(current)
					if e != nil {
						t.Fatal(n, e)
					}
					r := next.ProAuto.Replacement
					if r.RootEmail != root || r.Attempts != n || r.ProviderSwitched != (n > limit) {
						t.Fatal(r)
					}
					pay, key, e := s.ProRechargePay(r)
					if e != nil {
						t.Fatal(e)
					}
					if pay.PlanCode != "pro5" {
						t.Fatal("changed product", pay.PlanCode)
					}
					if n <= limit && (pay.Provider != gptpay.Tokenseek || key != "primary-key") {
						t.Fatal("switched early")
					}
					if n > limit && (pay.Provider != gptpay.CMSNav || key != "backup-key" || pay.Country != "US") {
						t.Fatal("wrong backup settings")
					}
					current = next.Email
					if n == limit+1 {
						if err = s.Close(); err != nil {
							t.Fatal(err)
						}
						s, err = Open(dir)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				failReplacement(t, s, current, 10)
				if _, err = s.ClaimProRechargeReplacement(current); err == nil {
					t.Fatal("exceeded total budget")
				}
				work, err := s.ProRechargeReplacementWork()
				if err != nil || len(work) != 0 {
					t.Fatal("exhausted chain still queued", len(work), err)
				}
				if backupLimit > 0 {
					pay, key, _ := s.GPTPaySettings()
					if pay.Provider != gptpay.CMSNav || pay.BackupProvider != gptpay.Tokenseek || key != "backup-key" || pay.PlanCode != "pro20" || pay.CardAccountLimit != 7 {
						t.Fatal("roles or saved settings damaged")
					}
					original, _, _ := s.MailAccountCredential(root)
					pay, key, _ = s.ProRechargePay(original.ProAuto.Replacement)
					if pay.Provider != gptpay.Tokenseek || key != "primary-key" {
						t.Fatal("old account rerouted")
					}
				}
			})
		}
	}
}

func TestRechargeFailoverAtomicConcurrentChains(t *testing.T) {
	s := transferStore(t)
	root := replacementAccounts(t, s, 8)
	setupRechargeFailover(t, s, root, 0, 2)
	other := "replace-1@example.com"
	setupRechargeFailover(t, s, other, 0, 2)
	failReplacement(t, s, root, 0)
	failReplacement(t, s, other, 0)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			email := root
			if i%2 == 1 {
				email = other
			}
			p, e := s.ClaimProRechargeReplacement(email)
			if e != nil || !p.ProAuto.Replacement.ProviderSwitched {
				t.Errorf("claim: %v", e)
			}
		}(i)
	}
	wg.Wait()
	a, _, _ := s.MailAccountCredential(root)
	b, _, _ := s.MailAccountCredential(other)
	if a.ProAuto.Replacement.NextEmail == b.ProAuto.Replacement.NextEmail {
		t.Fatal("same successor")
	}
	pay, _, _ := s.GPTPaySettings()
	if pay.Provider != gptpay.CMSNav || pay.BackupProvider != gptpay.Tokenseek {
		t.Fatal("swapped back")
	}
}

func TestRechargeFailoverGuardsRollbackAndRecovery(t *testing.T) {
	for _, mode := range []string{"no_account", "no_key", "no_terms", "changed_roles", "stopped", "no_backup"} {
		t.Run(mode, func(t *testing.T) {
			s := transferStore(t)
			root := replacementAccounts(t, s, 1)
			setupRechargeFailover(t, s, root, 0, 2)
			if mode != "no_account" {
				replacementAccounts(t, s, 3)
				setupRechargeFailover(t, s, root, 0, 2)
			}
			switch mode {
			case "no_key":
				_, _ = s.db.Exec(`UPDATE gptpay_provider_settings SET encrypted_key='' WHERE provider='cmsnav'`)
			case "no_terms":
				_, _ = s.db.Exec(`UPDATE gptpay_provider_settings SET profile=json_set(profile,'$.terms_accepted',json('false')) WHERE provider='cmsnav'`)
			case "changed_roles":
				pay, _, _ := s.GPTPaySettings()
				pay.BackupProvider = gptpay.XUPay
				if _, err := s.SaveGPTPaySettings(pay, ""); err != nil {
					t.Fatal(err)
				}
			case "no_backup":
				_, _ = s.UpdateProAccount(root, func(p *model.MailAccountProfile) { p.ProAuto.Replacement.BackupProvider = "" })
			}
			failReplacement(t, s, root, 0)
			if mode == "stopped" {
				if _, err := s.StopProRechargeReplacementChain(root); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ClaimProRechargeReplacement(root); err == nil {
				t.Fatal("guard allowed switch")
			}
			pay, _, _ := s.GPTPaySettings()
			if pay.Provider != gptpay.Tokenseek {
				t.Fatal("partial role switch")
			}
			p, _, _ := s.MailAccountCredential(root)
			if p.ProAuto.Replacement.NextEmail != "" {
				t.Fatal("partial account claim")
			}
			if mode == "no_account" {
				_, err := s.SaveMailAccount(model.MailAccountProfile{Email: "new@example.com", ManagementScope: "pro"}, model.MailAccountCredentials{Email: "new@example.com"})
				if err != nil {
					t.Fatal(err)
				}
				next, err := s.ClaimProRechargeReplacement(root)
				if err != nil || !next.ProAuto.Replacement.ProviderSwitched {
					t.Fatal(err)
				}
			}
			if strings.Contains(mustJSON(p), "primary-key") || strings.Contains(mustJSON(p), "backup-key") {
				t.Fatal("leaked secret")
			}
		})
	}
}

func TestRechargeFailoverRestockBudgetAndBreaker(t *testing.T) {
	s := transferStore(t)
	replacementAccounts(t, s, 4)
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID, c.FailureLimit = true, "auto", 1, 1
	c.HourLimit = 1
	c, err := s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ReserveProRestock(restock.Decision{ID: "batch", At: time.Now(), Version: c.Version, Suggested: 1}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	root := tasks[0]
	root.Status, root.Stage = "failed", "recharge"
	if err = s.SaveProRestockTask(root); err != nil {
		t.Fatal(err)
	}
	setupRechargeFailover(t, s, root.Email, 0, 2)
	failReplacement(t, s, root.Email, 0)
	if _, err = s.ClaimProRechargeReplacement(root.Email); err == nil {
		t.Fatal("ignored hourly limit")
	}
	pay, _, _ := s.GPTPaySettings()
	if pay.Provider != gptpay.Tokenseek {
		t.Fatal("switched before budget admission")
	}
	l, err := s.ProRestockLimits(time.Now())
	if err != nil || l.Failures != 0 {
		t.Fatal(l, err)
	}
	c.HourLimit = 10
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	root.Version = c.Version
	if err = s.SaveProRestockTask(root); err != nil {
		t.Fatal(err)
	}
	n, err := s.ClaimProRechargeReplacement(root.Email)
	if err != nil {
		t.Fatal(err)
	}
	if n.ProRestockTaskID == "" || n.ProRestockTaskID == root.ID {
		t.Fatal("not a distinct budgeted attempt", n)
	}
	task, err := s.ProRestockTask(n.ProRestockTaskID)
	if err != nil || task.DecisionID != root.DecisionID || task.Status != "reserved" {
		t.Fatal(task, err)
	}
	l, err = s.ProRestockLimits(time.Now())
	if err != nil || l.DayCount != 2 || l.Failures != 0 {
		t.Fatal(l, err)
	}
}

func TestRechargeFailoverRestockKeepsTemplateAndPlanGuards(t *testing.T) {
	s := transferStore(t)
	root := replacementAccounts(t, s, 4)
	setupRechargeFailover(t, s, root, 0, 2)
	// Clear the synthetic flow so the real restock reservation can claim it.
	_, err := s.UpdateProAccount(root, func(p *model.MailAccountProfile) { p.ProAuto = model.ProAutoState{} })
	if err != nil {
		t.Fatal(err)
	}
	p := model.DefaultProSettings()
	p.EJHPlanTemplates = map[string]string{"pro5": "template5"}
	if _, err = s.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ReserveProRestock(restock.Decision{ID: "template-chain", At: time.Now(), Version: c.Version, Suggested: 1, Snapshot: restock.Snapshot{NewPlan: "tokenseek:pro5:JP:0"}}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	task.Status, task.Stage = "failed", "recharge"
	if err = s.SaveProRestockTask(task); err != nil {
		t.Fatal(err)
	}
	setupRechargeFailover(t, s, task.Email, 0, 2)
	_, err = s.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) { p.ProAuto.Replacement.PurchaseRoute = task.PurchaseRoute })
	if err != nil {
		t.Fatal(err)
	}
	failReplacement(t, s, task.Email, 0)
	next, err := s.ClaimProRechargeReplacement(task.Email)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := s.ProRestockTask(next.ProRestockTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if successor.Plan != "cmsnav:pro5:US:0" || successor.PurchaseRoute.TemplateID != "template5" || successor.PurchaseRoute.PaymentProvider != gptpay.CMSNav {
		t.Fatal(successor)
	}
	successor.Status = "submitting"
	p.EJHPlanTemplates["pro5"] = "changed"
	if _, err = s.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	if s.ClaimProRestockDispatch(successor) == nil {
		t.Fatal("changed template admitted")
	}
	p.EJHPlanTemplates["pro5"] = "template5"
	if _, err = s.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimProRestockDispatch(successor); err != nil {
		t.Fatal("pinned replacement blocked by backup default plan", err)
	}
	if s.ClaimProRestockDispatch(successor) == nil {
		t.Fatal("duplicate dispatch admitted")
	}
}

func TestRechargeFailoverNextTaskUsesNewPrimaryAndCanSwitchBack(t *testing.T) {
	s := transferStore(t)
	root := replacementAccounts(t, s, 6)
	setupRechargeFailover(t, s, root, 0, 1)
	failReplacement(t, s, root, 0)
	next, err := s.ClaimProRechargeReplacement(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateProAccount(next.Email, func(p *model.MailAccountProfile) {
		p.ProAuto.Status = "completed"
		p.ProAuto.Replacement.Queued = false
	})
	if err != nil {
		t.Fatal(err)
	}
	pay, key, err := s.GPTPaySettings()
	if err != nil || pay.Provider != gptpay.CMSNav || key != "backup-key" {
		t.Fatal("new primary did not persist", err)
	}
	// A completely new task snapshots the now-active supplier; only its own
	// exhausted primary budget can exchange the roles again.
	newRoot := "replace-4@example.com"
	_, err = s.UpdateProAccount(newRoot, func(p *model.MailAccountProfile) {
		p.ProAuto.ID = "next-task"
		p.ProAuto.Replacement = &model.ProRechargeReplacement{RootEmail: newRoot, Limit: 1, FailoverLimit: 1, PrimaryProvider: pay.Provider, BackupProvider: pay.BackupProvider, Payment: model.RechargePayment(pay)}
	})
	if err != nil {
		t.Fatal(err)
	}
	failReplacement(t, s, newRoot, 1)
	first, err := s.ClaimProRechargeReplacement(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	pay, key, err = s.ProRechargePay(first.ProAuto.Replacement)
	if err != nil || pay.Provider != gptpay.CMSNav || key != "backup-key" || first.ProAuto.Replacement.ProviderSwitched {
		t.Fatal("new task did not use current primary", err)
	}
	failReplacement(t, s, first.Email, 1)
	second, err := s.ClaimProRechargeReplacement(first.Email)
	if err != nil {
		t.Fatal(err)
	}
	pay, key, err = s.ProRechargePay(second.ProAuto.Replacement)
	if err != nil || pay.Provider != gptpay.Tokenseek || key != "primary-key" || pay.PlanCode != "pro20" {
		t.Fatal("next task failed to switch back with its original product", err)
	}
	active, _, _ := s.GPTPaySettings()
	if active.Provider != gptpay.Tokenseek || active.BackupProvider != gptpay.CMSNav {
		t.Fatal("second role exchange did not persist")
	}
}
