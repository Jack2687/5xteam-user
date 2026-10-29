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

func TestPaymentRiskReplacementOnceAcrossRestartAndSecondRiskPause(t *testing.T) {
	for _, outcome := range []string{"risk", "unsettled_risk", "ready"} {
		t.Run(outcome, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			c := restock.DefaultConfig()
			c.Enabled, c.Mode, c.GroupID, c.FailureLimit = true, "auto", 1, 1
			c, err = s.SaveProRestockConfig(c)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 8; i++ {
				email := fmt.Sprintf("risk-%d@example.com", i)
				if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			d := restock.Decision{ID: "initial", At: now.Add(-time.Hour), Version: c.Version, Suggested: 1, Snapshot: restock.Snapshot{Binding: "pool", GroupID: 1}}
			tasks, err := s.ReserveProRestock(d, "", 0)
			if err != nil || len(tasks) != 1 {
				t.Fatal(tasks, err)
			}
			root := tasks[0]
			risk := func(task restock.Task, settled bool) (bool, error) {
				task.OrderID = "order-" + task.ID
				o := gptpay.Order{ID: task.OrderID, Email: task.Email, Status: "failed", Error: "OpenAI 支付风控拦截", CreatedAt: time.Now(), Remote: gptpay.RemoteOrder{ID: "remote-" + task.ID, Status: "failed"}}
				if settled {
					o.Remote.SettlementStatus = "settled"
				}
				if err := s.CreateGPTPayOrder(o, gptpay.Snapshot{}); err != nil {
					return false, err
				}
				return s.FinishProRestockPaymentRisk(task)
			}
			if paused, err := risk(root, true); err != nil || paused {
				t.Fatal(paused, err)
			}
			// Count in the fixture task's day, including runs just after midnight.
			limits, err := s.ProRestockLimits(root.CreatedAt.Add(time.Second))
			if err != nil || limits.Failures != 0 || limits.DayCount != 1 {
				t.Fatal(limits, err)
			}
			// Even strict batches allow exactly one replacement, never the normal 3.
			c.AllowSmall, c.MinBatch = false, 3
			c, err = s.SaveProRestockConfig(c)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			tasks, err = s.ProRestockTasks()
			if err != nil || len(tasks) != 1 || !tasks[0].PaymentRiskPending {
				t.Fatal(tasks, err)
			}
			d.Version, d.At, d.Suggested = c.Version, time.Now().Add(time.Second), 5
			d.Snapshot.At, d.Snapshot.CollectionStartedAt = d.At, d.At
			d.Snapshot.Valid, d.Snapshot.LoadValid = true, true
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					copy := d
					copy.ID = fmt.Sprintf("replacement-%d", i)
					s.ReserveProRestock(copy, "", 0)
				}(i)
			}
			wg.Wait()
			tasks, err = s.ProRestockTasks()
			if err != nil || len(tasks) != 2 {
				t.Fatal(tasks, err)
			}
			var replacement restock.Task
			for _, task := range tasks {
				if task.ID != root.ID {
					replacement = task
				}
				if task.PaymentRiskPending {
					t.Fatal("one-time allowance duplicated")
				}
			}
			if replacement.Email == root.Email || replacement.PaymentRiskSource != root.ID {
				t.Fatal(replacement)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "ready" {
				replacement.Status = "ready"
				if err = s.SaveProRestockTask(replacement); err != nil {
					t.Fatal(err)
				}
			} else if paused, err := risk(replacement, outcome == "risk"); err != nil || !paused {
				t.Fatal(paused, err)
			}
			got, err := s.ProRestockConfig()
			if err != nil || got.Enabled != (outcome == "ready") {
				t.Fatal(got, err)
			}
			if outcome != "ready" {
				if got.Version != c.Version+1 {
					t.Fatal("pause did not invalidate queued workers")
				}
				d.ID = "forbidden-third"
				d.Version = got.Version
				if added, err := s.ReserveProRestock(d, "", 0); err == nil || len(added) != 0 {
					t.Fatal("third account admitted", added, err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				got, _ = s.ProRestockConfig()
				if got.Enabled {
					t.Fatal("restart cleared risk pause")
				}
			}
			original, _, err := s.GPTPayOrder("order-" + root.ID)
			if err != nil || original.Status != "failed" || !original.PaymentReviewRequired() {
				t.Fatal("original payment changed", err)
			}
		})
	}
}

func TestPaymentRiskUncertainOrSuccessfulNeverCreatesReplacement(t *testing.T) {
	for _, status := range []string{"submission_unknown", "success", "failed"} {
		t.Run(status, func(t *testing.T) {
			s := transferStore(t)
			o := gptpay.Order{ID: "order", Email: "risk@example.com", Status: status, Error: "支付风控拦截", CreatedAt: time.Now(), Remote: gptpay.RemoteOrder{ID: "remote", Status: status}}
			if err := s.CreateGPTPayOrder(o, gptpay.Snapshot{}); err != nil {
				t.Fatal(err)
			}
			task := restock.Task{ID: "task", Email: o.Email, OrderID: o.ID, Status: "attention"}
			if _, err := s.db.Exec(`INSERT INTO pro_restock_tasks VALUES(?,?,?,?,?,?)`, task.ID, task.Email, task.Status, formatTime(time.Now()), 0, mustJSON(task)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.FinishProRestockPaymentRisk(task); err == nil {
				t.Fatal("uncertain/successful payment admitted")
			}
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM pro_restock_payment_risk`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
