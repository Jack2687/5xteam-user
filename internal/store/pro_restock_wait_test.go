package store

import (
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
)

func TestRestockPreparationWaitRetainsTaskAndProtectsSubmittedWork(t *testing.T) {
	for _, scenario := range []string{"wait", "payment", "failed_order", "issuance", "stopped", "profile_stopped", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			s, task := preparationFixture(t)
			before := task
			switch scenario {
			case "payment", "failed_order":
				o := transferOrder(task.Email, "unknown", "submission_unknown")
				if scenario == "failed_order" {
					o.Order.Status = "failed"
				}
				if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
					t.Fatal(err)
				}
			case "issuance":
				if err := s.EJHPut("operation", "own-unknown", ejh.Operation{ID: "own-unknown", RestockTaskID: task.ID, Method: "open", Status: "unknown"}); err != nil {
					t.Fatal(err)
				}
			case "stopped":
				now := time.Now()
				task.StoppedAt = &now
				if err := s.SaveProRestockTask(task); err != nil {
					t.Fatal(err)
				}
			case "stale":
				task.UpdatedAt = task.UpdatedAt.Add(-time.Second)
			case "profile_stopped":
				if _, err := s.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true }); err != nil {
					t.Fatal(err)
				}
			}
			next := time.Now().Add(37 * time.Second)
			task.Message = "original order needs review"
			ok, err := s.WaitUnsubmittedRestockPreparation(&task, next)
			if err != nil || ok != (scenario == "wait") {
				t.Fatal(scenario, ok, err)
			}
			if !ok {
				return
			}
			saved, err := s.ProRestockTask(task.ID)
			if err != nil || saved.Status != "reserved" || saved.Email != before.Email || saved.Cost != before.Cost || saved.SubmittedAt != nil || saved.NextCardCheckAt == nil || !saved.NextCardCheckAt.Equal(next) {
				t.Fatal("wait lost intent or budget", saved, err)
			}
			limits, err := s.ProRestockLimits(time.Now())
			if err != nil || limits.Failures != 0 || limits.DayCount != 1 {
				t.Fatal("wait counted as failure/replacement", limits, err)
			}
			// Monitoring recovery must not terminalize the queued wait.
			if done, err := s.FinishUnsubmittedRestockPreparation(&saved); err != nil || done {
				t.Fatal("monitor released wait", done, err)
			}
		})
	}
}

func TestRestockFailedPreparationEndsReplacementQueue(t *testing.T) {
	s, task := preparationFixture(t)
	_, err := s.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) {
		p.ProAuto.Status = "replacement_queued"
		p.ProAuto.Replacement = &model.ProRechargeReplacement{RootEmail: task.Email, Queued: true, Limit: 2}
	})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.FinishUnsubmittedRestockPreparation(&task); err != nil || !done {
		t.Fatal(done, err)
	}
	p, _, err := s.MailAccountCredential(task.Email)
	if err != nil || p.ProAuto.Replacement.Queued || p.ProAuto.Status != "failed" || p.ProAuto.Stage != "card" {
		t.Fatal("failed successor remains queued", p.ProAuto, err)
	}
	work, err := s.ProRechargeReplacementWork()
	if err != nil || len(work) != 0 {
		t.Fatal("terminal preparation is still dispatched", work, err)
	}
}

func TestRestockDecisionPaginationUsesIndex(t *testing.T) {
	s := transferStore(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT payload FROM pro_restock_decisions ORDER BY created_at DESC,id DESC LIMIT 10 OFFSET 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var a, b, c int
		var d string
		if err = rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		plan += d
	}
	if !strings.Contains(plan, "pro_restock_decisions_recent") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}
