package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProRestockScheduleCancelsPendingButTracksStarted(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier request") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	second := "schedule-second@example.com"
	if _, err := st.SaveMailAccount(model.MailAccountProfile{Email: second, ManagementScope: "pro"}, model.MailAccountCredentials{Email: second}); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID, c.EmptyGroupCount, c.UnitCost = true, "auto", 1, 2, 3
	c, err := st.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	pay, _, _ := st.GPTPaySettings()
	collections := 0
	var accounts []restock.Account
	s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
		collections++
		return restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity), Accounts: accounts}, nil
	}
	s.proMergeMu.Lock()
	s.proAutoRunning = p.Concurrency
	s.proMergeMu.Unlock()
	if err = s.tickProRestock(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ProRestockTasks()
	if err != nil || len(tasks) != 2 || tasks[0].Status != "reserved" || tasks[1].Status != "reserved" {
		t.Fatal(tasks, err)
	}
	first := tasks[0]
	now := time.Now()
	first.Status, first.SubmittedAt = "submitting", &now
	if err = st.ClaimProRestockDispatch(first); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpdateProAccount(first.Email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "already-started", Status: "waiting_quota", StageTimes: map[string]time.Time{"push:completed": now}}
		p.PushStatus, p.PushProvider, p.Sub2AccountID = "completed", "sub2", 88
	}); err != nil {
		t.Fatal(err)
	}
	local := now.In(beijingLocation)
	c.NoRestockEnabled = true
	c.NoRestockStart, c.NoRestockEnd = local.Add(-time.Hour).Format("15:04"), local.Add(time.Hour).Format("15:04")
	c.NoRestockWindows = []restock.DailyWindow{
		{Start: local.Add(3 * time.Hour).Format("15:04"), End: local.Add(4 * time.Hour).Format("15:04")},
		{Start: c.NoRestockStart, End: c.NoRestockEnd},
	}
	c, err = st.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	accounts = []restock.Account{{ID: 88, Ready: true, Capacity: 20}}
	if err = s.tickProRestock(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	tasks, err = st.ProRestockTasks()
	if err != nil || len(tasks) != 2 {
		t.Fatal(tasks, err)
	}
	for _, task := range tasks {
		if task.ID == first.ID {
			if task.Status != "ready" || task.ReadyAt == nil {
				t.Fatal("started task did not finish verification", task)
			}
		} else if task.Status != "cancelled" || !strings.Contains(task.Message, "每日禁补") || task.SubmittedAt != nil {
			t.Fatal("unstarted task escaped cancellation", task)
		}
	}
	if !s.proRestock.decision.NoRestockActive || s.proRestock.decision.Started != 0 || collections != 2 {
		t.Fatal("monitoring stopped or new task started")
	}
	// Manual evaluation must not bypass the window, even with an empty group.
	accounts = nil
	if err = s.tickProRestock(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if s.proRestock.decision.EmptyGroupNeed == 0 || s.proRestock.decision.Suggested != 0 {
		t.Fatal(s.proRestock.decision)
	}
	tasks, err = st.ProRestockTasks()
	if err != nil || len(tasks) != 2 {
		t.Fatal("manual evaluation created more tasks", tasks, err)
	}
	limits, err := st.ProRestockLimits(time.Now())
	if err != nil || limits.DayCost != 3 {
		t.Fatal("started budget lost or pending budget retained", limits, err)
	}
	records, err := st.ProRestockDecisions(1, 0)
	if err != nil || len(records) != 1 || !records[0].NoRestockActive {
		t.Fatal("missing persisted decision", records, err)
	}
	rec := httptest.NewRecorder()
	s.getProRestock(rec, httptest.NewRequest(http.MethodGet, "/api/pro-restock", nil))
	var response struct {
		Data struct {
			Active bool   `json:"no_restock_active"`
			Reason string `json:"no_restock_reason"`
		} `json:"data"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 200 || !response.Data.Active || !strings.Contains(response.Data.Reason, "每日禁补") {
		t.Fatal(rec.Body.String(), err)
	}
	// Simulate leaving the window without disabling the feature. Fresh demand
	// is assessed again, and the released account can be reserved normally.
	c.NoRestockStart, c.NoRestockEnd = local.Add(time.Hour).Format("15:04"), local.Add(2*time.Hour).Format("15:04")
	c.NoRestockWindows[1] = restock.DailyWindow{Start: c.NoRestockStart, End: c.NoRestockEnd}
	c, err = st.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	// Cooldown remains a separate protection.
	if err = s.tickProRestock(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if s.proRestock.decision.NoRestockActive || s.proRestock.decision.Suggested != 0 || !strings.Contains(s.proRestock.decision.Reason, "冷却") {
		t.Fatal(s.proRestock.decision)
	}
	after, _, _, _ := st.ProSettings()
	beforeJSON, _ := json.Marshal(p)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("legacy settings changed")
	}
}
