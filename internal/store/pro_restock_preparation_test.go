package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func preparationFixture(t *testing.T) (*Store, restock.Task) {
	t.Helper()
	s := transferStore(t)
	email := "preparation@example.com"
	if _, e := s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); e != nil {
		t.Fatal(e)
	}
	p := model.DefaultProSettings()
	p.CardProvider = "nexovcc"
	if _, e := s.SaveProSettings(p, "", ""); e != nil {
		t.Fatal(e)
	}
	c := restock.DefaultConfig()
	c.Enabled = true
	c.Mode = "auto"
	c.GroupID = 1
	c, e := s.SaveProRestockConfig(c)
	if e != nil {
		t.Fatal(e)
	}
	tasks, e := s.ReserveProRestock(restock.Decision{ID: "prepare", At: time.Now(), Version: c.Version, Suggested: 1}, "", 0)
	if e != nil || len(tasks) != 1 {
		t.Fatal(tasks, e)
	}
	task := tasks[0]
	task.Status = "submitting"
	now := time.Now()
	task.SubmittedAt = &now
	if e = s.ClaimProRestockDispatch(task); e != nil {
		t.Fatal(e)
	}
	return s, task
}
func TestRestockPreparationOnlyReleasesProvenUnsubmitted(t *testing.T) {
	for _, scenario := range []string{"no_intent", "failed", "unknown", "serial", "workflow", "payment", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			s, task := preparationFixture(t)
			switch scenario {
			case "failed", "unknown", "serial":
				op := nexovcc.Operation{ID: "op", Status: "failed", RestockTaskID: task.ID}
				if scenario == "unknown" {
					op.Status = "unknown"
				}
				if scenario == "serial" {
					op.Serial = "issued"
				}
				if _, e := s.db.Exec(`INSERT INTO nexovcc_operations(id,scope,profile,status) VALUES(?,?,?,?)`, op.ID, "test", mustJSON(op), op.Status); e != nil {
					t.Fatal(e)
				}
			case "workflow":
				s.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) { p.ProAuto.ID = "running" })
			case "payment":
				o := transferOrder(task.Email, "unknown", "submission_unknown")
				if e := s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
					t.Fatal(e)
				}
			case "stale":
				task.UpdatedAt = task.UpdatedAt.Add(-time.Second)
			}
			done, e := s.FinishUnsubmittedRestockPreparation(&task)
			if e != nil {
				t.Fatal(e)
			}
			want := scenario == "no_intent" || scenario == "failed"
			if done != want {
				t.Fatal(scenario, done)
			}
			if done {
				saved, _ := s.ProRestockTasks()
				if saved[0].Status != "failed" || saved[0].Cost != 0 {
					t.Fatal("cleanup not durable")
				}
			}
		})
	}
}
func TestBalanceFallbackPreservesRouteAndRefusesUncertainIssuance(t *testing.T) {
	for _, scenario := range []string{"safe", "unknown", "serial", "payment", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			s, task := preparationFixture(t)
			switch scenario {
			case "unknown", "serial":
				op := nexovcc.Operation{ID: "op", Status: "failed", RestockTaskID: task.ID}
				if scenario == "unknown" {
					op.Status = "unknown"
				} else {
					op.Serial = "issued"
				}
				if _, e := s.db.Exec(`INSERT INTO nexovcc_operations(id,scope,profile,status) VALUES(?,?,?,?)`, op.ID, "test", mustJSON(op), op.Status); e != nil {
					t.Fatal(e)
				}
			case "payment":
				o := transferOrder(task.Email, "unknown", "submission_unknown")
				if e := s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
					t.Fatal(e)
				}
			case "stopped":
				s.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
			}
			route := model.ProPurchaseRoute{CardProvider: "ejh", TemplateID: "20x", PlanCode: "pro20", Sub2GroupIDs: []int64{4, 11}}
			err := s.SwitchRestockCardForBalance(task.ID, "nexovcc", route)
			if scenario != "safe" {
				if err == nil {
					t.Fatal("unsafe fallback")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			saved, err := s.ProRestockDispatchTask(task.ID)
			if err != nil || saved.CardProvider != "ejh" || saved.BalanceFallbackFrom != "nexovcc" || saved.PurchaseRoute.TemplateID != "20x" || len(saved.PurchaseRoute.Sub2GroupIDs) != 2 {
				t.Fatal(saved, err)
			}
			if s.SwitchRestockCardForBalance(task.ID, "ejh", model.ProPurchaseRoute{CardProvider: "nexovcc"}) == nil {
				t.Fatal("fallback oscillation accepted")
			}
		})
	}
}
