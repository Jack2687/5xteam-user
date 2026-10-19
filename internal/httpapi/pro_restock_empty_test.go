package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRestockEmptyStartupDispatchAcrossTicks(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier request") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	p, password, _, err := st.ProSettings()
	if err != nil {
		t.Fatal(err)
	}
	p.Concurrency = 1
	if _, err = st.SaveProSettings(p, password, ""); err != nil {
		t.Fatal(err)
	}
	second := "second-start@example.com"
	if _, err = st.SaveMailAccount(model.MailAccountProfile{Email: second, ManagementScope: "pro"}, model.MailAccountCredentials{Email: second}); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.Enabled = true
	c.Mode = "auto"
	c.GroupID = 1
	c.EmptyGroupCount = 2
	c.AllowSmall = false
	c, err = st.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	pay, _, _ := st.GPTPaySettings()
	var accounts []restock.Account
	var collectionErr error
	s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
		return restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity), Accounts: accounts}, collectionErr
	}
	tick := func() {
		t.Helper()
		if err := s.tickProRestock(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}
	s.stopNotifications()
	s.notifications.queue = make(chan notificationEvent, 10)
	// Busy work slots leave the whole startup batch durably reserved.
	s.proMergeMu.Lock()
	s.proAutoRunning = 1
	s.proMergeMu.Unlock()
	tick()
	tasks, err := st.ProRestockTasks()
	if err != nil || len(tasks) != 2 {
		t.Fatalf("startup reserve: %+v %v", tasks, err)
	}
	for _, task := range tasks {
		if task.Status != "reserved" || !task.EmptyGroupStartup {
			t.Fatal(task)
		}
	}
	// A repeated empty snapshot must keep these reservations, not cancel them.
	tick()
	tasks, _ = st.ProRestockTasks()
	if len(tasks) != 2 || s.proRestock.decision.EmptyGroupNeed != 0 {
		t.Fatalf("duplicate or cancelled reservation: %+v", tasks)
	}
	if len(s.notifications.queue) != 1 {
		t.Fatalf("repeated checks sent %d restock notifications", len(s.notifications.queue))
	}
	if event := <-s.notifications.queue; event.Kind != "restock" || !strings.Contains(event.Text, "空分组启动 · 净缺口 2 个") || !strings.Contains(event.Text, "监控组实际 0 个账号") {
		t.Fatal(event)
	}
	// Simulate one original task arriving before the other obtains a slot.
	first := tasks[0]
	first.Status = "ready"
	first.AccountID = 88
	first.ReadyAt = new(time.Time)
	*first.ReadyAt = time.Now()
	if err = st.SaveProRestockTask(first); err != nil {
		t.Fatal(err)
	}
	accounts = []restock.Account{{ID: 88, Ready: true, Capacity: 20}}
	launches := make(chan string, 2)
	s.proAutoHooks = &proAutomationHooks{Session: func(_ context.Context, email string, _ func(string, map[string]any) (any, error)) error {
		launches <- email
		return errors.New("test stops before payment")
	}}
	s.proMergeMu.Lock()
	s.proAutoRunning = 0
	s.proMergeMu.Unlock()
	collectionErr = errors.New("monitor unavailable")
	tick()
	tasks, _ = st.ProRestockTasks()
	if tasks[1].Status != "reserved" {
		t.Fatal("collection failure must retain unsubmitted startup task", tasks)
	}
	select {
	case <-launches:
		t.Fatal("dispatched on failed collection")
	default:
	}
	collectionErr = nil
	tick()
	select {
	case launched := <-launches:
		if launched == first.Email {
			t.Fatal("restarted first account")
		}
		waitProAutoFinished(t, s, launched)
	case <-time.After(5 * time.Second):
		t.Fatal("remaining startup reservation was not dispatched after first arrival")
	}
	tasks, _ = st.ProRestockTasks()
	if len(tasks) != 2 || s.proRestock.decision.Started != 0 || s.proRestock.decision.EmptyGroupNeed != 0 {
		t.Fatalf("nonempty pool started more tasks: %+v", tasks)
	}
}

func TestProRestockEmptyStartupPauseCancelsReservation(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier request") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	c := restock.DefaultConfig()
	c.Enabled = true
	c.Mode = "auto"
	c.GroupID = 1
	c.EmptyGroupCount = 1
	c, err := st.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	pay, _, _ := st.GPTPaySettings()
	s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
		return restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}, nil
	}
	s.proMergeMu.Lock()
	s.proAutoRunning = p.Concurrency
	s.proMergeMu.Unlock()
	if err = s.tickProRestock(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	tasks, _ := st.ProRestockTasks()
	if len(tasks) != 1 || tasks[0].Status != "reserved" {
		t.Fatal(tasks)
	}
	c.Enabled = false
	if _, err = st.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	if err = s.tickProRestock(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	tasks, _ = st.ProRestockTasks()
	if len(tasks) != 1 || tasks[0].Status != "cancelled" {
		t.Fatal(tasks)
	}
}
