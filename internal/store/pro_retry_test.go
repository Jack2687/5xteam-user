package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"slices"
	"testing"
	"time"
)

func TestDetachFailedProRestock(t *testing.T) {
	for _, status := range []string{"failed", "no-order", "success", "processing", "submission_unknown", "wrong-flow", "ready", "paid-stage"} {
		t.Run(status, func(t *testing.T) {
			s := transferStore(t)
			email := "retry@example.com"
			transferSource(t, s, email)
			orderID := "old-order"
			if status == "no-order" {
				orderID = ""
			} else {
				payment := status
				if status == "wrong-flow" || status == "ready" || status == "paid-stage" {
					payment = "failed"
				}
				o := transferOrder(email, orderID, payment)
				if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProRestockTaskID = "intent"
				p.ProRestockTargetID = "original-target"
				p.ProAuto = model.ProAutoState{ID: "flow", OrderID: orderID, Status: "failed", Steps: map[string]string{}}
				if status == "paid-stage" {
					p.ProAuto.Steps["recharge"] = "completed"
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			task := restock.Task{ID: "intent", Email: email, Status: "unknown", OrderID: orderID, Cost: 100, CreatedAt: time.Now()}
			if status == "ready" {
				task.Status = "ready"
			}
			if _, err = s.db.Exec(`INSERT INTO pro_restock_tasks VALUES(?,?,?,?,?,?)`, task.ID, email, task.Status, formatTime(task.CreatedAt), task.Cost, mustJSON(task)); err != nil {
				t.Fatal(err)
			}
			flow := "flow"
			if status == "wrong-flow" {
				flow = "stale-flow"
			}
			err = s.DetachFailedProRestock(email, flow, orderID)
			p, _, _ := s.MailAccountCredential(email)
			tasks, _ := s.ProRestockTasks()
			if status == "failed" || status == "no-order" {
				if err != nil || p.ProRestockTaskID != "" || p.ProRestockTargetID != "original-target" || p.ProAuto.OrderID != orderID || len(tasks) != 1 || tasks[0].Status != "failed" {
					t.Fatal("failed claim not safely detached", err, p.ProRestockTaskID, tasks)
				}
				if status == "failed" && tasks[0].Cost != 100 {
					t.Fatal("historical budget lost")
				}
				if status == "no-order" && tasks[0].Cost != 0 {
					t.Fatal("unsubmitted payment counted as spent")
				}
			} else if err == nil || p.ProRestockTaskID != "intent" || tasks[0].Status != task.Status {
				t.Fatal("unconfirmed task was detached", err)
			}
		})
	}
}

func TestDetachFailedProRestockPreservesPushGroups(t *testing.T) {
	for _, existing := range [][]int64{nil, {2, 4, 10}} {
		s := transferStore(t)
		email := "groups@example.com"
		transferSource(t, s, email)
		_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.ProRestockTaskID, p.ProRestockTargetID = "intent", "original-target"
			p.ProAuto = model.ProAutoState{Sub2PushGroupIDs: existing}
		})
		if err != nil {
			t.Fatal(err)
		}
		task := restock.Task{ID: "intent", Email: email, Status: "attention", CardProvider: "nexovcc", CreatedAt: time.Now(), PurchaseRoute: &model.ProPurchaseRoute{Sub2GroupIDs: []int64{4, 10, 15}}}
		if _, err = s.db.Exec(`INSERT INTO pro_restock_tasks VALUES(?,?,?,?,?,?)`, task.ID, email, task.Status, formatTime(task.CreatedAt), 0, mustJSON(task)); err != nil {
			t.Fatal(err)
		}
		if err = s.DetachFailedProRestock(email, "", ""); err != nil {
			t.Fatal(err)
		}
		want := existing
		if len(want) == 0 {
			want = task.PurchaseRoute.Sub2GroupIDs
		}
		p, _, err := s.MailAccountCredential(email)
		if err != nil || p.ProRestockTaskID != "" || p.ProRestockTargetID != "original-target" || !slices.Equal(p.ProAuto.Sub2PushGroupIDs, want) {
			t.Fatal("lost persisted push intent", p.ProAuto, err)
		}
	}
}
