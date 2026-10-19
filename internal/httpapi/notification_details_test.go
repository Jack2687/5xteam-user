package httpapi

import (
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
)

func TestNotificationRestockShowsActiveDecisionEvidence(t *testing.T) {
	supply, average := 10.023, 7.128
	d := restock.Decision{Config: restock.DefaultConfig(), SupplyMinutes: &supply, TriggerMinutes: 11, TargetMinutes: 15, TimeNeed: 1}
	want := "预计供给不足 · 净缺口 1 个\n预计支撑 10.02 分钟；实际触发水位 ≤ 11 分钟；补后目标 15 分钟。"
	if got := restockNotificationReasons(d); got != want {
		t.Fatalf("time evidence differs from decision dialog: %s", got)
	}
	d.TimeNeed = 0
	d.ConcurrencyNeed, d.ReportedCapacity, d.Capacity, d.Used, d.Confirmations = 1, 80, 48, 33, 2
	d.Config.CapacityPercent, d.Config.TriggerPercent = 60, 70
	d.ForceMergeNeed = 2
	d.ForceMerge = restock.ForceMergeStats{Participants: 3, AverageMinutes: &average}
	got := restockNotificationReasons(d)
	for _, expected := range []string{"持续并发压力 · 净缺口 1 个", "原始容量 80 × 60% = 48", "触发 ≥ 33 并发（70% 向下取整）", "连续确认 2 / 2 次", "强制合并前接替 · 净缺口 2 个", "平均剩余 7.13 分钟"} {
		if !strings.Contains(got, expected) {
			t.Fatal(expected, got)
		}
	}
	if strings.Contains(got, "预计供给不足") || strings.Contains(got, "最低备用") {
		t.Fatal("inactive rule announced", got)
	}
	d = restock.Decision{Config: restock.DefaultConfig(), EmptyGroupNeed: 2, ReserveNeed: 1, Usable: 2, Trace: &restock.DecisionTrace{GroupInflight: 1, TimelyInflight: 3}}
	d.Config.EmptyGroupCount, d.Config.Reserve = 3, 6
	got = restockNotificationReasons(d)
	if !strings.Contains(got, "目标 6 个 − 当前可用 2 个 − 预计及时就绪在途 3 个") || !strings.Contains(got, "扣除同组全部在途 1 个") {
		t.Fatal(got)
	}
	d = restock.Decision{Config: restock.DefaultConfig(), ExitNeed: 1, Exit: &restock.ExitForecast{AtRisk: 1, CurrentCapacity: 100, AfterCapacity: 50, Demand: 60}}
	if got = restockNotificationReasons(d); !strings.Contains(got, "退出后容量 50，采用需求 60") {
		t.Fatal(got)
	}
}

func TestNotificationMergeCostMatchesManagementValues(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := model.DefaultProSettings()
	cfg.Sub2.URL, cfg.Sub2.Email = "http://sub2.invalid", "fixture@example.com"
	if _, err = st.SaveProSettings(cfg, "fixture-password", ""); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, notifications: notificationRuntime{queue: make(chan notificationEvent, 10)}}
	now := time.Now()
	p := model.MailAccountProfile{Email: "fixture@example.com", PushProvider: "sub2", Sub2AccountID: 42, CostProvider: "sub2", CostDownstreamIdentity: "42", CostConnectionIdentity: proCostConnectionIdentity(cfg.Sub2), CostCheckedAt: &now, CostDownstreamSnapshot: 28.8827, UserCostDownstreamIdentity: "42", UserCostDownstreamSnapshot: 5.7765}
	s.notifyMergeStarted(p)
	event := <-s.notifications.queue
	if !strings.Contains(event.Text, "消耗额度（标准）：$28.8827") || !strings.Contains(event.Text, "用户费用（已用）：$5.7765") {
		t.Fatal(event)
	}
	p.UserCostDownstreamIdentity = ""
	s.notifyMergeStarted(p)
	event = <-s.notifications.queue
	if !strings.Contains(event.Text, "用户费用（已用）：未查询") {
		t.Fatal("missing billing must not be displayed as zero", event)
	}
	p.Sub2AccountID = 99
	s.notifyMergeStarted(p)
	event = <-s.notifications.queue
	if strings.Contains(event.Text, "$28.8827") {
		t.Fatal("stale account binding exposed another account's cost", event)
	}
}

func TestNotificationEJHResultsDoNotExposeCardSecrets(t *testing.T) {
	s := &Server{notifications: notificationRuntime{queue: make(chan notificationEvent, 10)}}
	for _, tc := range []struct{ status, title string }{{"completed", "EJH 开卡成功"}, {"failed", "EJH 开卡失败"}, {"unknown", "EJH 开卡待核实"}, {"import_pending", "EJH 开卡待导入"}} {
		s.notifyEJHOpened(ejh.Operation{Method: "open", Status: tc.status, Amount: "1000", Currency: "PHP", Error: "private-secret", Result: &ejh.Card{Number: "4242424242424242", CVV: "939"}})
		event := <-s.notifications.queue
		if event.Title != tc.title || strings.Contains(event.Text, "private-secret") || strings.Contains(event.Text, "4242424242424242") || strings.Contains(event.Text, "939") {
			t.Fatal(event)
		}
	}
	s.notifyEJHOpened(ejh.Operation{Method: "cancel", Status: "completed"})
	if len(s.notifications.queue) != 0 {
		t.Fatal("cancellation announced as opening")
	}
}
