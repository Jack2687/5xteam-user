package httpapi

import (
	"chapt-space-user/internal/gptpay"
	"chapt-space-user/internal/model"
	"chapt-space-user/internal/restock"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"
)

func (s *Server) restockTasks(snap restock.Snapshot, p model.ProSettings, c restock.Config) ([]restock.Task, float64, error) {
	tasks, e := s.store.ProRestockTasks()
	if e != nil {
		return nil, 0, e
	}
	now := time.Now()
	byID := map[int64]restock.Account{}
	for _, a := range snap.Accounts {
		byID[a.ID] = a
	}
	seen := map[string]bool{}
	durations := []float64{}
	for i := range tasks {
		t := &tasks[i]
		if t.Active() {
			seen[t.Email] = true
		}
		if !t.Active() {
			if t.ReadyAt != nil && t.SubmittedAt != nil && t.Binding == snap.Binding {
				durations = append(durations, restockObservedLead(*t))
			}
			continue
		}
		profile, _, err := s.store.MailAccountCredential(t.Email)
		if err != nil {
			t.Status = "attention"
			t.Message = "账号无法读取，保留在途与预算等待核验"
			t.ETA = nil
			if err = s.store.SaveProRestockTask(*t); err != nil {
				return nil, 0, err
			}
			continue
		}
		if profile.ProAuto.OrderID != "" {
			t.OrderID = profile.ProAuto.OrderID
		}
		t.CardID, t.AccountID = profile.ProAuto.CardID, profile.Sub2AccountID
		if t.OrderID == "" && profile.ProAuto.ID != "" {
			order, _, lookupErr := s.store.GPTPayOrder("pro-auto-" + profile.ProAuto.ID)
			if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, 0, lookupErr
			}
			if lookupErr == nil {
				t.OrderID = order.ID
				_, err = s.store.UpdateProAccount(t.Email, func(p *model.MailAccountProfile) { p.ProAuto.OrderID = order.ID })
				if err != nil {
					return nil, 0, err
				}
			}
		}
		settled, err := s.restockExitSettled(profile, t.OrderID)
		if err != nil {
			return nil, 0, err
		}
		if settled {
			t.Status, t.Stage, t.ETA = "ready", "merge", nil
			t.UpdatedAt = now
			t.Message = "已完成推送并进入合并／移出阶段，不再占用补货在途；当前可用容量以监控组为准"
			// No readiness observation was made: do not invent ReadyAt and count
			// the account's entire consumption lifetime as provisioning latency.
			if err = s.store.SaveProRestockTask(*t); err != nil {
				return nil, 0, err
			}
			continue
		}
		if restockAccountExiting(profile) {
			t.Status, t.ETA = "unknown", nil
			t.UpdatedAt = now
			t.Message = "账号已进入合并／移出，但原付款记录尚未核清，保留订单与在途；不承诺新增容量"
			if err = s.store.SaveProRestockTask(*t); err != nil {
				return nil, 0, err
			}
			continue
		}
		if !profile.ProAuto.StopRequested && profile.ProAuto.Status == "interrupted" && profile.ProAuto.Steps["oauth"] == "completed" && !s.proAutomationRunning(t.Email) {
			next := time.Now()
			_, err = s.store.UpdateProAccount(t.Email, func(p *model.MailAccountProfile) {
				if !p.ProAuto.StopRequested && p.ProAuto.Status == "interrupted" {
					p.ProAuto.Status = "waiting_quota"
					p.ProAuto.NextCheckAt = &next
				}
			})
			if err == nil {
				s.queueDueProAutomation(t.Email)
			}
		}
		if t.Stages == nil {
			t.Stages = map[string]time.Time{}
		}
		for key, at := range profile.ProAuto.StageTimes {
			t.Stages[key] = at
		}
		if profile.ProAuto.Stage != "" {
			t.Stage = profile.ProAuto.Stage
			if _, ok := t.Stages[t.Stage]; !ok {
				t.Stages[t.Stage] = now
			}
		}
		verified := false
		if a, ok := byID[t.AccountID]; ok {
			verified = a.Ready && snap.Valid && t.Binding == snap.Binding && t.GroupID == snap.GroupID
		}
		if (t.Binding != snap.Binding || t.GroupID != snap.GroupID) && profile.PushStatus == "completed" {
			pinned, secret, _, pe := s.proAccountPushSettings(profile)
			if pe == nil {
				checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				accounts, ae := s.sub2.MonitorAccounts(checkCtx, pinned.Sub2, secret, t.GroupID)
				cancel()
				if ae == nil {
					for _, a := range accounts {
						if a.ID == t.AccountID && a.Ready(now, pinned.Sub2.Models) {
							verified = true
						}
					}
				}
			}
		}
		if verified && profile.PushStatus == "completed" {
			t.Status = "ready"
			t.ReadyAt = &now
			t.ETA = nil
			t.Message = "已核验 Sub2 账号、组归属、模型和可调度状态"
		} else if profile.ProAuto.ID != "" {
			t.Status = "running"
			t.Message = profile.ProAuto.DisplayError()
			if profile.PushStatus == "completed" {
				t.Status = "verifying"
			}
			if !s.proAutomationRunning(t.Email) && profile.ProAuto.Status != "waiting_quota" {
				t.Status = "attention"
				t.ETA = nil
				t.Message = "流程已停止，保留原订单与在途，请在 Pro 管理中核验或续跑"
			}
			if t.OrderID != "" {
				order, _, err := s.store.GPTPayOrder(t.OrderID)
				if err != nil || (order.Active() && (order.Status == "submission_unknown" || !s.proAutomationRunning(t.Email))) {
					t.Status = "unknown"
					t.ETA = nil
					t.Message = "原订单结果待确认，禁止重新付款"
				} else if order.Status == "failed" && !s.proAutomationRunning(t.Email) {
					t.Status = "failed"
					t.ETA = nil
				} else if order.Status == "success" && profile.ProAuto.Steps["oauth"] != "completed" && (profile.ProAuto.Status == "interrupted" || profile.ProAuto.Status == "failed") {
					s.queueRestockPaidRecovery(profile, p)
				}
			} else if t.Status == "attention" {
				t.Status = "failed"
				t.Cost = 0
			}
		} else if t.Status != "reserved" {
			t.Status = "attention"
			t.ETA = nil
			t.Message = "启动结果待核验，保留预留，避免重复发卡"
		}
		t.UpdatedAt = now
		if err = s.store.SaveProRestockTask(*t); err != nil {
			return nil, 0, err
		}
	}
	lead := float64(c.LeadMinutes)
	if len(durations) >= c.LeadSamples {
		sort.Float64s(durations)
		lead = durations[min(len(durations)-1, int(math.Ceil(float64(len(durations))*.9))-1)]
	}
	for _, profile := range s.store.MailAccountsByManagementScope("pro") {
		if seen[profile.Email] || profile.ProRestockTaskID != "" {
			continue
		}
		settled, err := s.restockExitSettled(profile, "")
		if err != nil {
			return nil, 0, err
		}
		if settled {
			continue
		}
		if profile.ProAuto.ID == "" {
			manual := false
			for _, stage := range profile.ProManualStages {
				if stage.Status == "running" {
					manual = true
				}
			}
			if profile.PushStatus != "completed" && profile.ProManualStages["recharge"].Status == "completed" {
				manual = true
			}
			order, err := s.store.ActiveGPTPayOrder(profile.Email)
			if err == nil {
				manual = true
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, 0, err
			}
			if manual {
				tasks = append(tasks, restock.Task{ID: "manual:" + profile.Email, Email: profile.Email, Status: "attention", Stage: "manual", OrderID: order.ID, Message: "手动任务占用在途；目标与就绪时间未经核验，不承诺未来供给"})
			}
			continue
		}
		if a, ok := byID[profile.Sub2AccountID]; ok && a.Ready && !restockAccountExiting(profile) {
			continue
		}
		target, _, _, targetErr := s.proAccountPushSettings(profile)
		if targetErr != nil || validateRestockTarget(c, target) != nil || restockBinding(target) != snap.Binding {
			continue
		}
		if profile.ProAuto.Status == "completed" && !restockAccountExiting(profile) {
			continue
		}
		running := s.proAutomationRunning(profile.Email)
		orderID := profile.ProAuto.OrderID
		if orderID == "" {
			// Recover the durable payment written just before its profile link.
			orderID = "pro-auto-" + profile.ProAuto.ID
		}
		order, _, orderErr := s.store.GPTPayOrder(orderID)
		if orderErr != nil && !errors.Is(orderErr, sql.ErrNoRows) {
			return nil, 0, orderErr
		}
		if !running && (orderErr != nil || order.Status == "failed") && profile.ProAuto.OrderID == "" && profile.ProAuto.Steps["recharge"] != "completed" && profile.ProAuto.Steps["oauth"] != "completed" && profile.PushStatus != "completed" {
			continue
		}
		if !running && orderErr == nil && order.Status == "failed" {
			continue
		}
		t := restock.Task{Plan: "payment:" + profile.ProAuto.PlanCode, ID: profile.ProAuto.ID, Email: profile.Email, Status: "external", Stage: profile.ProAuto.Stage, OrderID: profile.ProAuto.OrderID, CreatedAt: profile.ProAuto.StartedAt, GroupID: c.GroupID, Binding: snap.Binding, Message: "手动／原定时任务在途，占用共用工作并发"}
		if !running {
			t.Status = "attention"
			t.Message = "旧任务已停止但付款或就绪结果仍待核验，保留在途"
		}
		if orderErr == nil {
			t.OrderID = order.ID
			if order.Status == "submission_unknown" || order.Status == "manual_review" {
				t.Status = "unknown"
				t.Message = "原订单结果待确认，保留在途且不承诺未来容量"
			}
		}
		if restockAccountExiting(profile) {
			t.Status = "unknown"
			t.Message = "账号已进入合并／移出，但原付款记录尚未核清，保留订单与在途；不承诺新增容量"
		}
		t.SubmittedAt = &profile.ProAuto.StartedAt
		t.Plan = profile.ProAuto.QuotaPlan
		if profile.ProAuto.RestockBinding == "" {
			t.Binding = ""
			t.Status = "attention"
			t.Message = "旧任务缺少原推送绑定，保留在途但不承诺当前池供给"
		} else if profile.ProAuto.RestockBinding != snap.Binding {
			continue
		}
		tasks = append(tasks, t)
	}
	s.proMergeMu.Lock()
	busy := s.proAutoRunning
	s.proMergeMu.Unlock()
	s.proAutoMu.Lock()
	jobs := len(s.proAutoJobs)
	s.proAutoMu.Unlock()
	queued := max(busy, jobs)
	for i := range tasks {
		t := &tasks[i]
		if !t.Active() || t.Status == "unknown" || t.Status == "attention" {
			continue
		}
		remaining := lead
		if t.SubmittedAt != nil {
			remaining = restockRemainingLead(*t, tasks, c.LeadSamples, lead, now)
			if remaining <= 0 {
				t.ETA = nil
				t.Message = "已超过预计到货时间；仍占在途，不承诺未来容量"
				if t.DecisionID != "" {
					_ = s.store.SaveProRestockTask(*t)
				}
				continue
			}
		} else {
			remaining += float64(queued/max(1, p.Concurrency)) * lead
			queued++
		}
		eta := now.Add(time.Duration(remaining * float64(time.Minute)))
		t.ETA = &eta
		if t.DecisionID != "" {
			if err := s.store.SaveProRestockTask(*t); err != nil {
				return nil, 0, err
			}
		}
	}
	return tasks, lead * (1 + float64(queued/max(1, p.Concurrency))), nil
}

var errProRestockBusy = errors.New("正在采集或更新补货配置，请稍后重试")

func (s *Server) tickProRestock(ctx context.Context, force bool) error {
	if !s.proRestock.tick.TryLock() {
		return errProRestockBusy
	}
	defer s.proRestock.tick.Unlock()
	now := time.Now()
	c, e := s.store.ProRestockConfig()
	if e != nil {
		return e
	}
	if !force && now.Before(s.proRestock.next) {
		s.sampleRestockMoneyBetweenChecks(c, now)
		return nil
	}
	// Retry after the configured interval, even when a failed request was slow.
	defer func() { s.proRestock.next = time.Now().Add(time.Duration(c.CheckSeconds) * time.Second) }()
	tasks, e := s.store.ProRestockTasks()
	if e != nil {
		return e
	}
	active := false
	for _, t := range tasks {
		if t.Active() {
			active = true
		}
	}
	if !force && !c.Enabled && !active {
		return nil
	}
	p, password, _, e := s.store.ProSettings()
	if e != nil {
		return e
	}
	sampleCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var snap restock.Snapshot
	if s.proRestock.monitor != nil {
		snap, e = s.proRestock.monitor(sampleCtx, c, p, password)
	} else {
		snap, e = s.collectProRestock(sampleCtx, c, p, password)
	}
	if e != nil {
		snap.Valid = false
		snap.Error = e.Error()
	}
	if !snap.Valid || !snap.LoadValid {
		last, le := s.store.ProRestockSnapshot()
		if le == nil && last.Binding == restockBinding(p) && last.GroupID == c.GroupID && last.Valid && last.LoadValid {
			last.Valid, last.LoadValid, last.Error = false, false, snap.Error
			snap = last
		}
	}
	if snap.ID == "" {
		snap.ID = randomRegistrationID()
	}
	snap.GroupID = c.GroupID
	if snap.Binding == "" {
		snap.Binding = restockBinding(p)
	}
	tasks, lead, e := s.restockTasks(snap, p, c)
	if e != nil {
		return e
	}
	tasks, e = s.store.ReconcileForceMerge(c, snap, tasks)
	if e != nil {
		return e
	}
	s.proRestock.mu.Lock()
	if c.Version != s.proRestock.lastVersion || snap.Binding != s.proRestock.lastSnapshot.Binding || !snap.Valid || !snap.LoadValid {
		s.proRestock.confirmation = 0
		s.proRestock.exitConfirmation = 0
	}
	snap.ExitDemand = restock.ObserveExitDemand(c, snap, s.proRestock.lastSnapshot, time.Now())
	fresh := snap.At.After(s.proRestock.lastSample)
	if fresh {
		if restock.Pressure(c, snap) {
			s.proRestock.confirmation++
		} else {
			s.proRestock.confirmation = 0
		}
		exit := restock.ForecastExits(c, snap, tasks, p.Sub2.AccountConcurrency, lead, time.Now())
		if c.ExitEnabled && exit.Valid && exit.Pressure {
			s.proRestock.exitConfirmation++
		} else {
			s.proRestock.exitConfirmation = 0
		}
		s.proRestock.lastSample = snap.At
	}
	snap.ExitConfirmations = s.proRestock.exitConfirmation
	confirm := s.proRestock.confirmation
	s.proRestock.lastVersion = c.Version
	s.proRestock.mu.Unlock()
	// Collection and reconciliation can outlive the snapshot's freshness window.
	now = time.Now()
	limits, e := s.store.ProRestockLimits(now)
	if e != nil {
		return e
	}
	d := restock.Evaluate(c, snap, tasks, limits, confirm, p.Sub2.AccountConcurrency, lead, now)
	d.ID = randomRegistrationID()
	committed := []restock.Task{}
	for _, t := range tasks {
		if t.Status != "reserved" {
			committed = append(committed, t)
		}
	}
	gross := restock.Evaluate(c, snap, committed, restock.Limits{}, confirm, p.Sub2.AccountConcurrency, lead, now)
	drop := gross.DemandKnown && !restock.Pressure(c, snap) && gross.TimeNeed == 0 && gross.ReserveNeed == 0 && gross.EmptyGroupNeed == 0 && gross.ForceMergeNeed == 0 && gross.ExitNeed == 0 && (!c.ExitEnabled || (gross.Exit != nil && gross.Exit.Complete && !gross.Exit.Pressure)) && (!c.TimeEnabled || gross.SupplyMinutes != nil || (d.Used == 0 && d.Waiting == 0))
	keepReserved := gross.Suggested
	startupPending := false
	for _, t := range tasks {
		if t.Status == "reserved" && d.NoRestockActive {
			if err := s.store.CancelProRestock(t, "进入每日禁补时段，撤销未启动预留；结束后按新快照重新评估"); err != nil {
				return err
			}
			continue
		}
		excess := t.Status == "reserved" && gross.DemandKnown && gross.Suggested > 0 && keepReserved <= 0
		if t.Status == "reserved" && (!c.Enabled || c.Mode != "auto" || t.Version != c.Version || t.Binding != snap.Binding || t.GroupID != snap.GroupID || (snap.Valid && t.Plan != snap.NewPlan) || (!t.EmptyGroupStartup && t.ForceMergeSource == "" && (drop || excess)) || (t.ForceMergeSource != "" && !c.ForceMergeEnabled)) {
			_ = s.store.CancelProRestock(t, "配置变化、暂停或新快照确认需求回落")
		} else if t.Status == "reserved" {
			keepReserved--
			startupPending = startupPending || t.EmptyGroupStartup || t.ForceMergeSource != ""
		}
	}
	payConfig, _, purchaseErr := s.store.GPTPaySettings()
	if purchaseErr == nil {
		purchaseErr = gptpay.ValidatePurchase(payConfig)
	}
	if purchaseErr != nil {
		d.Suggested, d.Reason = 0, purchaseErr.Error()
	}
	if purchaseErr == nil && c.Enabled && c.Mode == "auto" && d.Suggested > 0 && fresh && snap.Valid && ctx.Err() == nil {
		target, e := s.store.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro-restock", Provider: "sub2", Sub2: p.Sub2, Secret: password})
		if e == nil {
			var reserved []restock.Task
			s.proCardAdmissionMu.Lock()
			reserved, e = s.store.ReserveProRestock(d, target, d.Inflight)
			s.proCardAdmissionMu.Unlock()
			d.Started = len(reserved)
			if e == nil && d.Started > 0 {
				s.queueNotification("restock", "自动补货已安排", fmt.Sprintf("监控分组：%d\n本次补货：%d 个账号\n触发原因：%s", c.GroupID, d.Started, d.Reason))
			}
		}
		if e != nil {
			d.Reason = e.Error()
		}
	}
	// Finish the already-reserved startup batch as slots free up, even after
	// its first account arrives. This never reserves extra startup accounts.
	if startupPending && gross.DemandKnown && gross.Suggested == 0 {
		d.Reason += "；启动或提前接替批次仍有预留，按工作并发继续执行"
	}
	if purchaseErr == nil && c.Enabled && c.Mode == "auto" && !d.NoRestockActive && snap.Valid && snap.LoadValid && (gross.Suggested > 0 || (startupPending && gross.DemandKnown)) && limits.Failures < c.FailureLimit && ctx.Err() == nil {
		s.dispatchProRestock(ctx, c, p, snap, gross.Suggested == 0)
	}
	s.proRestock.mu.Lock()
	s.proRestock.lastSnapshot = snap
	s.proRestock.decision = d
	s.proRestock.tasks = append([]restock.Task{}, tasks...)
	record := force || d.Started > 0 || s.proRestock.lastReason != d.Reason || now.Sub(s.proRestock.lastRecord) >= 30*time.Second
	if record {
		s.proRestock.lastReason = d.Reason
		s.proRestock.lastRecord = now
	}
	s.proRestock.mu.Unlock()
	if snap.Valid && snap.LoadValid {
		_ = s.store.SaveProRestockSnapshot(snap)
	}
	if record {
		_ = s.store.SaveProRestockDecision(d)
		_ = s.store.PurgeProRestock(c.RetentionDays)
	}
	return nil
}
func (s *Server) dispatchProRestock(ctx context.Context, c restock.Config, p model.ProSettings, snap restock.Snapshot, startupOnly bool) {
	tasks, e := s.store.ProRestockTasks()
	if e != nil {
		return
	}
	pay, _, e := s.store.GPTPaySettings()
	if e != nil {
		return
	}
	for _, t := range tasks {
		if startupOnly && !t.EmptyGroupStartup && t.ForceMergeSource == "" {
			continue
		}
		if t.Status != "reserved" || t.Version != c.Version || t.Binding != snap.Binding || t.Plan != restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity) {
			continue
		}
		profile, _, readErr := s.store.MailAccountCredential(t.Email)
		if readErr != nil {
			return
		}
		if profile.ProAuto.StopRequested {
			_ = s.store.CancelProRestock(t, "用户已停止账号流程，撤销未提交预留")
			continue
		}
		if !restockSnapshotFresh(c, snap, time.Now()) {
			return
		}
		s.proMergeMu.Lock()
		busy := s.proAutoRunning
		s.proMergeMu.Unlock()
		s.proAutoMu.Lock()
		jobs := len(s.proAutoJobs)
		closed := s.proAutoClosed
		s.proAutoMu.Unlock()
		if closed || max(busy, jobs) >= p.Concurrency || ctx.Err() != nil {
			return
		}
		cards, _, e := s.store.GPTPayCards(1, 0, true)
		if e != nil {
			return
		}
		card := ""
		if !p.EJHAutoOpen {
			if len(cards) == 0 {
				t.Message = "银行卡没有剩余名额，等待卡片释放"
				_ = s.store.SaveProRestockTask(t)
				return
			}
			card = cards[0].ID
		}
		now := time.Now()
		if !restockSnapshotFresh(c, snap, now) || c.NoRestockReason(now) != "" {
			return
		}
		t.Status = "submitting"
		t.SubmittedAt = &now
		t.UpdatedAt = now
		if e = s.store.ClaimProRestockDispatch(t); e != nil {
			return
		}
		body, _ := json.Marshal(map[string]any{"provider": pay.Provider, "plan_code": pay.PlanCode, "card_id": card, "country": pay.Country, "credit_quantity": pay.CreditQuantity})
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(context.WithValue(ctx, proRestockContextKey{}, t.ID))
		r.SetPathValue("email", t.Email)
		w := httptest.NewRecorder()
		s.startProAutomation(w, r)
		if w.Code != http.StatusAccepted {
			t.Status = "attention"
			t.Message = "启动未获确认；请检查账号和卡片记录，保留预算与在途"
			var reply struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(w.Body.Bytes(), &reply) == nil && reply.Error != "" {
				t.Message = reply.Error
			}
			_ = s.store.SaveProRestockTask(t)
		}
	}
}

func restockSnapshotFresh(c restock.Config, snap restock.Snapshot, now time.Time) bool {
	return snap.Valid && snap.LoadValid && !snap.At.IsZero() && now.Sub(snap.At) <= time.Duration(c.FreshSeconds)*time.Second && !snap.At.After(now.Add(5*time.Second))
}
func restockObservedLead(t restock.Task) float64 {
	started := t.SubmittedAt
	if started == nil || t.ReadyAt == nil {
		return 0
	}
	return math.Max(.5, t.ReadyAt.Sub(*started).Minutes())
}

// Use the most recent observed milestone, anchored to its original timestamp.
// A stalled task must not get a new ETA merely because the monitor ran again.
func restockRemainingLead(t restock.Task, history []restock.Task, minimum int, fallback float64, now time.Time) float64 {
	remaining := fallback - now.Sub(*t.SubmittedAt).Minutes()
	key := ""
	var at time.Time
	for stage, reached := range t.Stages {
		if reached.After(at) {
			key, at = stage, reached
		}
	}
	if key == "" {
		return remaining
	}
	samples := []float64{}
	for _, prior := range history {
		if prior.ReadyAt == nil || prior.Binding != t.Binding || prior.Plan != t.Plan {
			continue
		}
		reached, ok := prior.Stages[key]
		if ok && prior.ReadyAt.After(reached) {
			samples = append(samples, prior.ReadyAt.Sub(reached).Minutes())
		}
	}
	if len(samples) < minimum {
		return remaining
	}
	sort.Float64s(samples)
	p90 := samples[min(len(samples)-1, int(math.Ceil(float64(len(samples))*.9))-1)]
	return math.Max(.25, p90) - now.Sub(at).Minutes()
}
