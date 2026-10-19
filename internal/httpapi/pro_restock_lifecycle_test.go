package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"net/http"
	"testing"
	"time"
)

func TestRestockExternalMergeIsNotIncomingSupply(t *testing.T) {
	for _, tc := range []struct {
		name, stage, payment                 string
		ready, absent, stopped, manual, want bool
	}{
		{name: "quota_exhausted_merging", stage: "merge", payment: "success"},
		{name: "timeout_merging_still_schedulable", stage: "merge", payment: "success", ready: true},
		{name: "removed_from_group", stage: "merge", payment: "success", absent: true},
		{name: "failed_merge_tail", stage: "merge", payment: "success", stopped: true},
		{name: "manual_four_steps", payment: "success", manual: true},
		{name: "push_still_waiting_for_verification", stage: "quota", payment: "success", want: true},
		{name: "new_login_with_old_push_flags", stage: "login", payment: "success", want: true},
		{name: "unknown_payment_even_if_merge_running", stage: "merge", payment: "submission_unknown", want: true},
		{name: "unknown_payment_even_if_still_schedulable", stage: "merge", payment: "submission_unknown", ready: true, want: true},
		{name: "review_payment", stage: "merge", payment: "manual_review", want: true},
		{name: "missing_linked_order", stage: "merge", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier call") })
			s.proAutoCancel()
			s.proAutoWG.Wait()
			configureProAutoFixture(t, s)
			p, _, _, err := st.ProSettings()
			if err != nil {
				t.Fatal(err)
			}
			if tc.payment != "" {
				if err = st.CreateGPTPayOrder(gptpay.Order{ID: "pro-auto-lifecycle", Email: email, CardID: "test-card", Status: tc.payment, CreatedAt: time.Now()}, gptpay.Snapshot{}); err != nil {
					t.Fatal(err)
				}
			}
			_, err = st.UpdateProAccount(email, func(v *model.MailAccountProfile) {
				v.PushProvider, v.PushStatus, v.Sub2AccountID = "sub2", "completed", 88
				v.ProAuto = model.ProAutoState{ID: "lifecycle", OrderID: "pro-auto-lifecycle", Stage: tc.stage, Status: "running", StartedAt: time.Now().Add(-30 * time.Minute), RestockBinding: restockBinding(p), Steps: map[string]string{"recharge": "completed", "oauth": "completed", "push": "completed"}}
				if tc.stage == "merge" {
					v.ProAuto.Steps["quota"] = "completed"
				}
				if tc.stopped {
					v.ProAuto.Status = "failed"
					v.ProAuto.Steps["merge"] = "failed"
				}
				if tc.manual {
					v.ProAuto = model.ProAutoState{}
					v.ProWorkflowRunning = true
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.stopped && !tc.manual {
				s.proAutoJobs = map[string]context.CancelFunc{email: func() {}}
				defer delete(s.proAutoJobs, email)
			}
			c := restock.DefaultConfig()
			c.GroupID = 1
			c.EmptyGroupCount = 1
			snap := restock.Snapshot{ID: "lifecycle", At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p)}
			if !tc.absent {
				snap.Accounts = []restock.Account{{ID: 88, Ready: tc.ready, Capacity: 50}}
			}
			for range 2 {
				tasks, lead, err := s.restockTasks(snap, p, c)
				if err != nil {
					t.Fatal(err)
				}
				if (len(tasks) > 0) != tc.want {
					t.Fatalf("incoming=%+v want=%v", tasks, tc.want)
				}
				d := restock.Evaluate(c, snap, tasks, restock.Limits{}, 0, 50, lead, time.Now())
				if !tc.want && d.Inflight != 0 {
					t.Fatal("exiting account still consumes inflight", d)
				}
				if tc.absent && d.EmptyGroupNeed != 1 {
					t.Fatal("old merging account suppressed empty-group replenishment", d)
				}
				if tc.want && tc.stage == "merge" && (tasks[0].Predictable() || tasks[0].OrderID != "pro-auto-lifecycle") {
					t.Fatal("uncertain payment lost its original hold", tasks)
				}
			}
		})
	}
}

func TestRestockPersistedMergeFinishesDeliveryOnly(t *testing.T) {
	for _, payment := range []string{"success", "submission_unknown", "manual_review", "missing"} {
		t.Run(payment, func(t *testing.T) {
			s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier call") })
			s.proAutoCancel()
			s.proAutoWG.Wait()
			configureProAutoFixture(t, s)
			p, _, _, _ := st.ProSettings()
			c := restock.DefaultConfig()
			c.Enabled = true
			c.Mode = "auto"
			c.GroupID = 1
			c.MinBatch = 1
			c.MaxBatch = 1
			c.UnitCost = 25
			c, err := st.SaveProRestockConfig(c)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			snap := restock.Snapshot{ID: "persisted", At: now, Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p)}
			tasks, err := st.ReserveProRestock(restock.Decision{ID: "persisted-exit", At: now, Version: c.Version, Snapshot: snap, Suggested: 1}, "target", 0)
			if err != nil || len(tasks) != 1 {
				t.Fatal(tasks, err)
			}
			if payment != "missing" {
				if err = st.CreateGPTPayOrder(gptpay.Order{ID: "pro-auto-persisted", Email: email, CardID: "test-card", Status: payment, CreatedAt: now}, gptpay.Snapshot{}); err != nil {
					t.Fatal(err)
				}
			}
			_, err = st.UpdateProAccount(email, func(v *model.MailAccountProfile) {
				v.PushProvider, v.PushStatus, v.Sub2AccountID = "sub2", "completed", 88
				v.ProAuto = model.ProAutoState{ID: "persisted", OrderID: "pro-auto-persisted", Status: "running", Stage: "merge", RestockBinding: restockBinding(p), StartedAt: now.Add(-time.Hour), Steps: map[string]string{"oauth": "completed", "push": "completed", "quota": "completed"}}
				v.ProWorkflowRunning = true
			})
			if err != nil {
				t.Fatal(err)
			}
			task := tasks[0]
			task.Status = "running"
			task.SubmittedAt = &now
			task.OrderID = "pro-auto-persisted"
			if err = st.SaveProRestockTask(task); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				tasks, _, err = s.restockTasks(snap, p, c)
				if err != nil || len(tasks) != 1 {
					t.Fatal(tasks, err)
				}
				got := tasks[0]
				if payment == "success" {
					if got.Active() || got.Status != "ready" || got.ReadyAt != nil || got.ETA != nil {
						t.Fatal("delivered account still inflight or fabricated readiness latency", got)
					}
				} else if !got.Active() || got.Status != "unknown" || got.Predictable() {
					t.Fatal("uncertain order was released", got)
				}
			}
			limits, err := st.ProRestockLimits(now)
			if err != nil || limits.DayCost != 25 {
				t.Fatal("lost purchase budget/history", limits, err)
			}
			profile, _, err := st.MailAccountCredential(email)
			if err != nil || !profile.ProWorkflowRunning || profile.ProAuto.Stage != "merge" || profile.ProAuto.OrderID != "pro-auto-persisted" {
				t.Fatal("changed the merge or payment workflow", profile.ProAuto, err)
			}
			linked, err := st.ProRestockDecisionTasks("persisted-exit")
			if err != nil || len(linked) != 1 || linked[0].OrderID != "pro-auto-persisted" {
				t.Fatal("lost decision/task audit", linked, err)
			}
		})
	}
}
