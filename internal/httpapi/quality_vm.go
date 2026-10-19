package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/vm2api"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

func vmQualityEligible(p model.FreeAccountProfile) bool {
	return accountProvider(p) == "vm2api" && p.VM2SlotID != "" && p.VM2AssignmentID != "" && p.DownstreamConnectionID != "" && p.StandardRemoval == nil && !p.Dead && p.AcceptStatus == "completed" && p.PushStatus == "completed" && p.RemoveStatus != "completed" && p.RemoteRemovedAt == nil
}
func vmQualityCleanupPending(p model.FreeAccountProfile) bool {
	return accountProvider(p) == "vm2api" && p.Quality.SourceProvider == "vm2api" && p.Quality.Action == "kick" && p.Quality.ActionStarted && p.RemoveStatus != "completed"
}
func vmQualityIdentity(p model.FreeAccountProfile) string {
	return fmt.Sprintf("%s|%s|%s|%s", p.DownstreamConnectionID, p.CycleID, p.VM2SlotID, p.VM2AssignmentID)
}
func (s *Server) vmQualityTargetMatches(p model.FreeAccountProfile, cfg model.VM2APISettings) bool {
	if accountProvider(p) != "vm2api" || p.DownstreamConnectionID == "" {
		return false
	}
	target, err := s.store.DownstreamTarget(p.DownstreamConnectionID)
	return err == nil && target.Provider == "vm2api" && target.VM2API.URL == cfg.URL
}
func (s *Server) vmQualityPolicyCurrent(q model.QualitySettings, cfg model.VM2APISettings) bool {
	current, err := s.store.QualitySettings("vm2api")
	if err != nil || !current.Enabled || current.Revision != q.Revision {
		return false
	}
	push, _, err := s.store.Sub2Settings()
	if err != nil || providerForSettings(push) != "vm2api" {
		return false
	}
	vm, _, err := s.store.VM2APISettings()
	return err == nil && vm.URL == cfg.URL
}
func vmQualityFresh(q model.QualitySettings, p model.FreeAccountProfile, snapshot vm2api.QualitySnapshot, slot vm2api.QualitySlot) bool {
	v := slot.Result
	if snapshot.Version != 1 || snapshot.Revision == "" || slot.Error != "" || slot.ID != p.VM2SlotID || slot.AssignmentID != p.VM2AssignmentID || slot.Identity == "" || !slot.Current || v == nil || v.ID < 1 || v.Revision != snapshot.Revision {
		return false
	}
	if v.Status != "normal" && v.Status != "degraded" && v.Status != "error" {
		return false
	}
	if v.Source != "manual" && v.Source != "auto" {
		return false
	}
	since := p.CreatedAt
	for _, at := range []*time.Time{p.JoinedAt, p.PushedAt} {
		if at != nil && at.After(since) {
			since = *at
		}
	}
	return !v.StartedAt.IsZero() && !v.StartedAt.Before(since) && !v.CheckedAt.Before(v.StartedAt) && !v.CheckedAt.After(snapshot.ServerNow.Add(5*time.Second)) && snapshot.ServerNow.Sub(v.CheckedAt) <= qualityMaxAge(q)
}
func (s *Server) runVMQualityBatch(ctx context.Context, force bool, onlyID string) {
	profiles, err := s.store.VMQualityAccounts()
	if err != nil {
		return
	}
	q, err := s.store.QualitySettings("vm2api")
	if err != nil {
		return
	}
	// Once a removal starts it belongs to the saved cycle/connection. Disabling
	// queries or switching providers must not abandon an already-started cleanup.
	for _, p := range profiles {
		if ctx.Err() != nil {
			return
		}
		if vmQualityCleanupPending(p) && (onlyID == "" || onlyID == p.ID) && (force || p.Quality.NextAt == nil || !time.Now().Before(*p.Quality.NextAt)) {
			lock, _ := s.freeLocks.LoadOrStore(p.ID, &sync.Mutex{})
			mutex := lock.(*sync.Mutex)
			if mutex.TryLock() {
				current, _, e := s.store.FreeAccountCredential(p.ID)
				if e == nil && current.CycleID == p.CycleID && vmQualityCleanupPending(current) {
					s.finishVMQualityAction(ctx, current, q, nil)
				}
				mutex.Unlock()
			}
		}
	}
	cfg, key, err := s.store.VM2APISettings()
	if err != nil || !s.vmQualityPolicyCurrent(q, cfg) {
		return
	}
	jobs := []model.FreeAccountProfile{}
	for _, p := range profiles {
		if !vmQualityEligible(p) || p.Quality.ActionStarted || !s.vmQualityTargetMatches(p, cfg) || (onlyID != "" && p.ID != onlyID) {
			continue
		}
		if force || p.Quality.Revision != q.Revision || p.Quality.Identity != vmQualityIdentity(p) || p.Quality.NextAt == nil || !time.Now().Before(*p.Quality.NextAt) {
			jobs = append(jobs, p)
		}
	}
	s.qualityMu.Lock()
	s.qualityRuntime = qualityRuntime{Running: true, Total: len(jobs), Active: map[string]bool{}}
	s.qualityMu.Unlock()
	defer func() { s.qualityMu.Lock(); s.qualityRuntime.Running = false; s.qualityMu.Unlock() }()
	client := vm2api.New(cfg, key)
	for start := 0; start < len(jobs); start += 100 {
		if ctx.Err() != nil || !s.vmQualityPolicyCurrent(q, cfg) {
			return
		}
		end := start + 100
		if end > len(jobs) {
			end = len(jobs)
		}
		refs := []vm2api.QualitySlotRef{}
		seen := map[string]bool{}
		for _, p := range jobs[start:end] {
			if !seen[p.VM2SlotID] {
				refs = append(refs, vm2api.QualitySlotRef{ID: p.VM2SlotID, AssignmentID: p.VM2AssignmentID})
				seen[p.VM2SlotID] = true
			}
		}
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		snapshot, readErr := client.QualityResults(readCtx, refs)
		cancel()
		byID := map[string]vm2api.QualitySlot{}
		for _, v := range snapshot.Slots {
			byID[v.ID] = v
		}
		queue := make(chan model.FreeAccountProfile)
		var wg sync.WaitGroup
		workers := q.Concurrency
		if workers < 1 {
			workers = 1
		}
		if workers > 8 {
			workers = 8
		}
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range queue {
					s.applyVMQuality(ctx, p, q, cfg, snapshot, byID[p.VM2SlotID], readErr)
					s.qualityMu.Lock()
					s.qualityRuntime.Done++
					s.qualityMu.Unlock()
				}
			}()
		}
		for _, p := range jobs[start:end] {
			queue <- p
		}
		close(queue)
		wg.Wait()
	}
}
func (s *Server) applyVMQuality(ctx context.Context, initial model.FreeAccountProfile, q model.QualitySettings, cfg model.VM2APISettings, snapshot vm2api.QualitySnapshot, slot vm2api.QualitySlot, readErr error) {
	if ctx.Err() != nil {
		return
	}
	lock, _ := s.freeLocks.LoadOrStore(initial.ID, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	if !mutex.TryLock() {
		return
	}
	defer mutex.Unlock()
	p, _, err := s.store.FreeAccountCredential(initial.ID)
	if err != nil || vmQualityIdentity(p) != vmQualityIdentity(initial) || !vmQualityEligible(p) || p.Quality.ActionStarted || !s.vmQualityPolicyCurrent(q, cfg) || !s.vmQualityTargetMatches(p, cfg) {
		return
	}
	s.qualityMu.Lock()
	if s.qualityRuntime.Active == nil {
		s.qualityRuntime.Active = map[string]bool{}
	}
	s.qualityRuntime.Active[p.ID] = true
	s.qualityMu.Unlock()
	defer func() { s.qualityMu.Lock(); delete(s.qualityRuntime.Active, p.ID); s.qualityMu.Unlock() }()
	state := p.Quality
	next := time.Now().UTC().Add(time.Duration(qualityPollSeconds(q)) * time.Second)
	state.SourceProvider = "vm2api"
	state.Revision = q.Revision
	state.Identity = vmQualityIdentity(p)
	state.NextAt = &next
	state.SourceFresh = false
	state.SourceFailureLimit = 1
	state.SourceRecovered = false
	state.Status = "unknown"
	state.QuestionStatus = "unknown"
	state.Degraded = false
	state.QuestionDegraded = false
	state.Failures = 0
	state.Successes = 0
	state.Error = ""
	state.Action = ""
	state.ActionStatus = ""
	state.Excluded = false
	same := true
	if readErr != nil {
		state.Status = "error"
		state.QuestionStatus = "error"
		state.Error = "vm2api 降智结果读取失败: " + readErr.Error()
	} else if !vmQualityFresh(q, p, snapshot, slot) {
		state.Error = "暂无当前账号及配置的有效结果，或结果已过期；不执行处理"
	} else {
		v := slot.Result
		version := fmt.Sprintf("%s|%s|%d", slot.Identity, v.Revision, v.ID)
		same = state.SourceVersion == version
		state.SourceVersion = version
		state.SourceFresh = true
		state.Status = v.Status
		state.QuestionStatus = v.Status
		state.CheckedAt = &v.CheckedAt
		state.SourceQuestionAt = &v.CheckedAt
		state.QuestionID = v.QuestionID
		state.QuestionName = v.QuestionName
		state.Answer = v.Answer
		state.Reason = v.Reason
		state.DurationMS = v.DurationMS
		state.SourceModel = v.Model
		state.ContentPassed = v.ContentPassed
		state.TimePassed = v.TimePassed
		state.Degraded = v.Status == "degraded"
		state.QuestionDegraded = state.Degraded
		if state.Degraded {
			state.Failures = 1
			if q.Action == "kick" {
				state.Action = "kick"
				state.ActionStatus = "pending"
			}
		}
		if v.Status == "normal" {
			state.Successes = 1
			state.SourceRecovered = true
		}
		if v.Status == "error" {
			state.Error = v.Reason
		}
	}
	p, err = s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(a *model.FreeAccountProfile) { a.Quality = state })
	if err != nil {
		return
	}
	if !same {
		s.auditAccountEvent(ctx, p.ID, "quality_remote_result", "quality", "quality_monitor", "vm2api", "读取 vm2api 降智结果："+state.Status, map[string]any{"slot_id": p.VM2SlotID, "version": state.SourceVersion, "result": slot.Result})
	}
	if q.Action == "kick" && state.Degraded && state.SourceFresh {
		s.finishVMQualityAction(ctx, p, q, func(latest model.FreeAccountProfile) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if vmQualityIdentity(latest) != state.Identity || !vmQualityEligible(latest) || !s.vmQualityPolicyCurrent(q, cfg) || !s.vmQualityTargetMatches(latest, cfg) {
				return errors.New("降智处理配置或账号绑定已改变，取消本次踢出")
			}
			// Re-read after waiting for the Team lock: a newer green/yellow result,
			// reset, replacement or config edit invalidates the original red result.
			c, e := s.vmClient(latest)
			if e != nil {
				return e
			}
			checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			current, e := c.QualityResults(checkCtx, []vm2api.QualitySlotRef{{ID: latest.VM2SlotID, AssignmentID: latest.VM2AssignmentID}})
			if e != nil {
				return e
			}
			if len(current.Slots) != 1 || !vmQualityFresh(q, latest, current, current.Slots[0]) || current.Slots[0].Result.Status != "degraded" || current.Slots[0].Result.ID != slot.Result.ID || current.Slots[0].Identity != slot.Identity || !s.vmQualityPolicyCurrent(q, cfg) {
				return errors.New("最新降智结果已改变或失效，取消本次踢出")
			}
			_, e = s.store.UpdateFreeAccountCycle(latest.ID, latest.CycleID, func(a *model.FreeAccountProfile) {
				a.Quality.ActionStarted = true
				a.Quality.Excluded = true
				a.RemovalReason = "vm2api 最新有效检测结果为降智，由母号强制踢出"
			})
			return e
		})
	}
}
func (s *Server) finishVMQualityAction(ctx context.Context, p model.FreeAccountProfile, q model.QualitySettings, guard func(model.FreeAccountProfile) error) {
	if guard == nil {
		guard = func(current model.FreeAccountProfile) error {
			if vmQualityIdentity(current) != vmQualityIdentity(p) || !vmQualityCleanupPending(current) {
				return errors.New("账号轮转周期或清理绑定已改变")
			}
			return nil
		}
	}
	_, err := s.performFreeAccountRemovalGuarded(ctx, p.ID, true, guard)
	next := time.Now().UTC().Add(time.Duration(q.RetrySeconds) * time.Second)
	status := "completed"
	message := ""
	if err != nil {
		status = "failed"
		message = err.Error()
		if errors.Is(err, errStandardRemovalPending) {
			status = "pending"
		}
	}
	_, saveErr := s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(a *model.FreeAccountProfile) {
		a.Quality.ActionStatus = status
		a.Quality.Error = message
		a.Quality.NextAt = &next
	})
	if saveErr == nil {
		s.auditAccountEvent(ctx, p.ID, "quality_action", "quality", "quality_monitor", "vm2api", "降智处理：母号强制踢出 / "+status, map[string]any{"error": message, "slot_id": p.VM2SlotID, "forced_mother_kick": true})
	}
}
func (s *Server) vmQualityStatus() map[string]any {
	q, err := s.store.QualitySettings("vm2api")
	s.qualityMu.Lock()
	runtime := s.qualityRuntime
	runtime.Active = map[string]bool{}
	for id, v := range s.qualityRuntime.Active {
		runtime.Active[id] = v
	}
	s.qualityMu.Unlock()
	out := map[string]any{"provider": "vm2api", "settings": q, "runtime": runtime, "server_now": time.Now().UTC(), "next_at": nil, "eligible": 0}
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	cfg, _, err := s.store.VM2APISettings()
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	profiles, err := s.store.VMQualityAccounts()
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	summary := model.QualitySummary{}
	var next *time.Time
	eligible := 0
	for _, p := range profiles {
		if !s.vmQualityTargetMatches(p, cfg) {
			continue
		}
		if p.RemoteRemovedAt != nil && p.Quality.SourceProvider == "vm2api" && p.Quality.Action == "kick" {
			summary.Kicked++
			if p.DownstreamCleaned {
				summary.Processed++
			}
		}
		if !vmQualityEligible(p) && !vmQualityCleanupPending(p) {
			continue
		}
		summary.Total++
		eligible++
		if p.Quality.Revision == q.Revision {
			switch p.Quality.Status {
			case "normal":
				summary.Normal++
			case "degraded":
				summary.Degraded++
			case "error":
				summary.Errors++
			}
		}
		if p.Quality.CheckedAt != nil && (summary.LastCheckedAt == nil || p.Quality.CheckedAt.After(*summary.LastCheckedAt)) {
			summary.LastCheckedAt = p.Quality.CheckedAt
		}
		due := p.Quality.NextAt
		if due == nil || p.Quality.Revision != q.Revision {
			now := time.Now().UTC()
			due = &now
		}
		if next == nil || due.Before(*next) {
			next = due
		}
	}
	out["summary"] = summary
	out["eligible"] = eligible
	if q.Enabled {
		out["next_at"] = next
	}
	return out
}
