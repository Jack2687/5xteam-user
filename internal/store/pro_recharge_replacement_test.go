package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func replacementAccounts(t *testing.T, s *Store, n int) string {
	t.Helper()
	for i := 0; i < n; i++ {
		email := fmt.Sprintf("replace-%d@example.com", i)
		if _, err := s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	return "replace-0@example.com"
}

func failReplacement(t *testing.T, s *Store, email string, limit int) model.MailAccountProfile {
	t.Helper()
	p, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		if p.ProAuto.Replacement == nil {
			p.ProAuto.ID = "flow"
			p.ProAuto.Replacement = &model.ProRechargeReplacement{RootEmail: email, Limit: limit}
		}
		if p.ProAuto.ID == "" {
			p.ProAuto.ID = "flow-" + p.Email
		}
		p.ProAuto.Replacement.Queued = false
		p.ProAuto.Stage, p.ProAuto.Status = "recharge", "failed"
		p.ProAuto.Steps = map[string]string{"login": "completed", "recharge": "failed"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QueueProRechargeReplacement(email, p.ProAuto.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRechargeReplacementBoundedConcurrentAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	root := replacementAccounts(t, s, 5)
	failReplacement(t, s, root, 2)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := s.ClaimProRechargeReplacement(root)
			if e != nil || p.Email != "replace-1@example.com" {
				t.Errorf("duplicate claim: %s %v", p.Email, e)
			}
		}()
	}
	wg.Wait()
	first, _, _ := s.MailAccountCredential(root)
	if first.ProAuto.OrderID != "" || first.ProAuto.Status != "failed" {
		t.Fatal("source mutated", first.ProAuto)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	failReplacement(t, s, first.ProAuto.Replacement.NextEmail, 9) // limit cannot be reset by a new attempt
	second, err := s.ClaimProRechargeReplacement(first.ProAuto.Replacement.NextEmail)
	if err != nil {
		t.Fatal(err)
	}
	if second.ProAuto.Replacement.Attempts != 2 || second.ProAuto.Replacement.Limit != 2 {
		t.Fatal(second.ProAuto)
	}
	failReplacement(t, s, second.Email, 9)
	if _, err = s.ClaimProRechargeReplacement(second.Email); err == nil {
		t.Fatal("exceeded limit")
	}
	p, _, _ := s.MailAccountCredential(second.Email)
	if p.ProAuto.Replacement.Pending {
		t.Fatal("exhausted remains pending")
	}
}

func TestRechargeReplacementOnlySecondStepAndNeverSuccess(t *testing.T) {
	for _, tc := range []struct {
		stage, status, order string
		limit                int
		want                 bool
	}{
		{"login", "failed", "", 2, false}, {"oauth", "failed", "", 2, false}, {"recharge", "interrupted", "", 2, false}, {"recharge", "failed", "", 0, false},
		{"recharge", "failed", "", 2, true}, {"recharge", "failed", "failed", 2, true}, {"recharge", "failed", "submission_unknown", 2, true}, {"recharge", "failed", "success", 2, false},
	} {
		t.Run(tc.stage+tc.status+tc.order+fmt.Sprint(tc.limit), func(t *testing.T) {
			s := transferStore(t)
			root := replacementAccounts(t, s, 2)
			a := model.ProAutoState{ID: "flow", Stage: tc.stage, Status: tc.status, Replacement: &model.ProRechargeReplacement{RootEmail: root, Limit: tc.limit}}
			if tc.order != "" {
				a.OrderID = "order"
				if err := s.CreateGPTPayOrder(gptpay.Order{ID: "order", Email: root, Status: tc.order, CreatedAt: time.Now()}, gptpay.Snapshot{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.UpdateProAccount(root, func(p *model.MailAccountProfile) { p.ProAuto = a }); err != nil {
				t.Fatal(err)
			}
			_ = s.QueueProRechargeReplacement(root, a.ID)
			p, _, _ := s.MailAccountCredential(root)
			if p.ProAuto.Replacement.Pending != tc.want {
				t.Fatal(p.ProAuto)
			}
			if tc.want {
				n, e := s.ClaimProRechargeReplacement(root)
				if e != nil || n.ProAuto.Stage != "login" || n.ProAuto.OrderID != "" {
					t.Fatal(n, e)
				}
			}
		})
	}
}

func TestRechargeReplacementStopAndNoAccount(t *testing.T) {
	s := transferStore(t)
	root := replacementAccounts(t, s, 1)
	failReplacement(t, s, root, 2)
	if _, err := s.ClaimProRechargeReplacement(root); err == nil {
		t.Fatal("invented an account")
	}
	p, _, _ := s.MailAccountCredential(root)
	if p.ProAuto.Replacement.Attempts != 0 || !p.ProAuto.Replacement.Pending {
		t.Fatal(p.ProAuto)
	}
	if _, err := s.SaveMailAccount(model.MailAccountProfile{Email: "spare@example.com", ManagementScope: "pro"}, model.MailAccountCredentials{Email: "spare@example.com"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.ClaimProRechargeReplacement(root)
	if err != nil {
		t.Fatal(err)
	}
	emails, err := s.StopProRechargeReplacementChain(n.Email)
	if err != nil || len(emails) != 2 {
		t.Fatal(emails, err)
	}
	work, err := s.ProRechargeReplacementWork()
	if err != nil || len(work) != 0 {
		t.Fatal(work, err)
	}
}

func TestRechargeReplacementRestockBudgetAndBreaker(t *testing.T) {
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
	failReplacement(t, s, root.Email, 2)
	if _, err = s.ClaimProRechargeReplacement(root.Email); err == nil {
		t.Fatal("ignored hourly limit")
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
