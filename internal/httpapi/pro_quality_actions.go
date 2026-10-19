package httpapi

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
)

func proQualityObservation(p model.MailAccountProfile, q model.ProQualitySettings, c model.ProSettings, snapshot sub2.RemoteQualitySnapshot, v sub2.RemoteQualityResult, readErr error) (model.ProQualityState, bool, bool) {
	state := p.ProQuality
	identity := proQualityIdentity(p, c)
	if state.Identity != identity {
		state = model.ProQualityState{}
	}
	if state.Revision != q.Revision && !state.ActionStarted {
		state.MergeAttempts, state.NextMergeAt = 0, nil
	}
	now := time.Now().UTC()
	next := now.Add(time.Duration(q.PollIntervalSeconds) * time.Second)
	state.Revision, state.Identity, state.SourceProvider = q.Revision, identity, "sub2"
	state.NextAt, state.FetchedAt = &next, &now
	state.SourceFresh, state.SourceRecovered = false, false
	state.ValidUntil = nil
	if readErr != nil {
		state.Status, state.Error = "error", "Sub2 降智结果读取失败："+readErr.Error()
		return state, false, false
	}
	bad, recovered := remoteQualityDecisionSince(q.DecisionSettings(), proQualitySince(p), snapshot, v)
	state.SourceVersion, state.SourceFailureLimit = v.Revision+"|"+v.Version, snapshot.Settings.FailureLimit
	state.SourceModel, state.SourceQuestionAt, state.SourceModelAt = snapshot.Settings.ModelAuditModel, v.Question.EvidenceAt, v.Model.EvidenceAt
	state.CheckedAt, state.QuestionStatus = v.Question.CheckedAt, v.Question.Status
	state.QuestionID, state.QuestionName, state.Answer = v.Question.QuestionID, v.Question.QuestionName, v.Question.Answer
	state.DurationMS, state.Reason, state.Failures, state.Successes = v.Question.DurationMS, v.Question.Reason, v.Question.Failures, v.Question.Successes
	state.QuestionDegraded, state.ContentPassed, state.TimePassed = v.Question.Degraded, &v.Question.ContentPassed, &v.Question.TimePassed
	state.ModelAudit = model.ModelAuditState{Status: v.Model.Status, Failures: v.Model.Failures, Successes: v.Model.Successes,
		Degraded: v.Model.Degraded, CheckedAt: v.Model.CheckedAt, SampleAt: v.Model.EvidenceAt,
		SentModel: v.Model.SentModel, ResponseModel: v.Model.ResponseModel, NoNewSamples: v.Model.NoNewSamples, Error: v.Model.Error, Revision: q.Revision}
	state.SourceFresh, state.SourceRecovered, state.Degraded = bad || recovered, recovered, bad
	if state.SourceFresh {
		for _, part := range []struct {
			selected, enabled bool
			v                 sub2.RemoteQualityVerdict
		}{{q.QuestionEnabled, snapshot.Settings.QuestionEnabled, v.Question.RemoteQualityVerdict}, {q.ModelAuditEnabled, snapshot.Settings.ModelAuditEnabled, v.Model.RemoteQualityVerdict}} {
			if !part.selected || !part.enabled || !remoteQualityFresh(part.v, proQualitySince(p), snapshot.ServerNow, time.Duration(q.MaxResultAgeSeconds)*time.Second) || bad && !part.v.Degraded {
				continue
			}
			deadline := now.Add(part.v.EvidenceAt.Add(time.Duration(q.MaxResultAgeSeconds) * time.Second).Sub(snapshot.ServerNow))
			if state.ValidUntil == nil || (bad && q.ConditionMode != "all" && deadline.After(*state.ValidUntil)) || ((!bad || q.ConditionMode == "all") && deadline.Before(*state.ValidUntil)) {
				state.ValidUntil = &deadline
			}
		}
	}
	state.Error = ""
	switch {
	case bad:
		state.Status = "degraded"
	case recovered:
		state.Status = "normal"
	default:
		state.Status = "unknown"
		state.Error = "检测尚未确认、无当前账号样本或 Sub2 检测未启用，不执行处理"
		for _, part := range []struct {
			selected bool
			v        sub2.RemoteQualityVerdict
		}{{q.QuestionEnabled, v.Question.RemoteQualityVerdict}, {q.ModelAuditEnabled, v.Model.RemoteQualityVerdict}} {
			if !part.selected {
				continue
			}
			if part.v.Error != "" {
				state.Status, state.Error = "error", part.v.Error
				break
			}
			if part.v.EvidenceAt != nil && snapshot.ServerNow.Sub(*part.v.EvidenceAt) > time.Duration(q.MaxResultAgeSeconds)*time.Second {
				state.Status, state.Error = "stale", "检测结果已过期，不执行处理"
			} else if state.Status == "unknown" && part.v.Status == "suspect" {
				state.Status = "suspect"
			}
		}
	}
	return state, bad, recovered
}

func (s *Server) applyProQuality(ctx context.Context, initial model.MailAccountProfile, q model.ProQualitySettings, c model.ProSettings, password string, snapshot sub2.RemoteQualitySnapshot, v sub2.RemoteQualityResult, readErr error) {
	if ctx.Err() != nil {
		return
	}
	value, _ := s.proLocks.LoadOrStore("account:"+initial.Email, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return
	}
	handedOff := false
	defer func() {
		if !handedOff {
			lock.Unlock()
		}
	}()
	p, _, err := s.store.MailAccountCredential(initial.Email)
	if err != nil || !proQualityEligible(p, c) || p.ProWorkflowRunning || proQualityIdentity(initial, c) != proQualityIdentity(p, c) || !s.proQualityPolicyCurrent(q, c) {
		return
	}
	state, bad, recovered := proQualityObservation(p, q, c, snapshot, v, readErr)
	if proQualityCanAct(p) {
		if q.Action == "merge" && (bad || state.Action == "merge" && state.ActionStarted) && (state.Action != "merge" || state.ActionStatus != "completed") {
			state.Action = "merge"
			if state.MergeAttempts > c.RetryCount {
				state.ActionStatus, state.ActionError = "failed", "降智合并重试次数已用完，请在账号操作中重试合并"
			} else if state.NextMergeAt == nil || !time.Now().Before(*state.NextMergeAt) {
				state.ActionStatus, state.ActionError = "pending", ""
			}
		} else if q.Action == "groups" && bad && (!state.Routed || state.Action == "restore" && state.ActionStatus != "completed") {
			state.Action, state.ActionStatus = "groups", "pending"
		} else if q.Action == "groups" && recovered && q.AutoRestore && (state.Routed || state.PlanReady) {
			state.Action, state.ActionStatus = "restore", "pending"
		} else if !state.ActionStarted && state.ActionStatus == "pending" {
			state.ActionStatus = "cancelled"
		}
	}
	updated, err := s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) { p.ProQuality = state })
	if err != nil {
		return
	}
	if p.ProQuality.SourceVersion != state.SourceVersion || p.ProQuality.Status != state.Status {
		s.auditProEvent(updated, "quality_result", state.Status, "sub2", "读取 Sub2 降智结果", map[string]any{"question": v.Question, "model": v.Model, "error": state.Error, "bad": bad, "recovered": recovered})
	}
	if !proQualityCanAct(updated) || s.proLoginAlreadyRunning(p.Email) || state.ActionStatus != "pending" || !s.proQualityPolicyCurrent(q, c) {
		return
	}
	if state.Action == "merge" {
		// Persist attempts before dispatch; a restart cannot create an unbounded retry loop.
		_, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
			p.ProQuality.MergeAttempts++
			next := time.Now().Add(time.Duration(max(q.PollIntervalSeconds, max(30, c.RetryIntervalSeconds))) * time.Second)
			p.ProQuality.NextMergeAt, p.ProQuality.ActionStatus = &next, "running"
		})
		if err != nil {
			return
		}
		request := &proQualityMergeRequest{Revision: q.Revision, Identity: state.Identity}
		if _, err = s.startProMergeJobWithQuality(p.Email, lock.Unlock, request); err == nil {
			handedOff = true
			s.auditProEvent(updated, "quality_action", "pending", "sub2", "降智已确认，跳过额度等待并执行空间合并", nil)
		} else {
			s.finishProQualityMerge(p.Email, request, err)
		}
		return
	}
	if readErr == nil && ((state.Action == "groups" && bad) || (state.Action == "restore" && recovered)) {
		s.applyProQualityGroups(ctx, updated, q, c, password)
	}
}

func (s *Server) applyProQualityGroups(ctx context.Context, p model.MailAccountProfile, q model.ProQualitySettings, c model.ProSettings, password string) {
	state := p.ProQuality
	groups, err := s.sub2.AccountGroups(ctx, c.Sub2, password, p.Sub2AccountID)
	if err == nil {
		if state.Action == "groups" {
			if !state.PlanReady {
				state.RemovedGroups = intersectQualityGroups(groups, q.NormalGroupIDs)
				state.AddedGroups = transformQualityGroups(q.DegradedGroupIDs, groups, nil)
				state.AssignGroups = slices.Clone(q.DegradedGroupIDs)
				state.PlanReady = true
			}
			state.TargetGroups = transformQualityGroups(groups, state.RemovedGroups, state.AssignGroups)
		} else {
			state.TargetGroups = transformQualityGroups(groups, state.AddedGroups, state.RemovedGroups)
		}
		state.ActionStatus = "running"
		_, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) { p.ProQuality = state })
		if err == nil && !s.proQualityPolicyCurrent(q, c) {
			err = errors.New("降智配置已变化，本次切组已取消")
		}
		if err == nil {
			latest, _, readErr := s.store.MailAccountCredential(p.Email)
			if readErr != nil || !proQualityCanAct(latest) || !proQualityEligible(latest, c) || proQualityIdentity(latest, c) != state.Identity || state.ValidUntil == nil || !time.Now().Before(*state.ValidUntil) {
				err = errors.New("账号状态已变化或检测结果过期，本次切组已取消")
			}
		}
		if err == nil {
			// A timed out write can already have succeeded. Preserve unrelated groups
			// and the original restore baseline across retries and process restarts.
			slices.Sort(groups)
			if !slices.Equal(groups, state.TargetGroups) {
				err = s.sub2.SetAccountGroups(ctx, c.Sub2, password, p.Sub2AccountID, state.TargetGroups)
			}
		}
	}
	if err != nil {
		state.ActionStatus, state.ActionError = "failed", err.Error()
	} else {
		state.ActionStatus, state.ActionError = "completed", ""
		state.Routed = state.Action == "groups"
		if state.Action == "restore" {
			state.PlanReady = false
			state.RemovedGroups, state.AddedGroups, state.AssignGroups, state.TargetGroups = nil, nil, nil, nil
		}
	}
	_, _ = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) { p.ProQuality = state })
	s.auditProEvent(p, "quality_action", state.ActionStatus, "sub2", "降智分组处理："+state.Action, map[string]any{"group_ids": state.TargetGroups, "error": state.ActionError})
}

type proQualityMergeKey struct{}
type proQualityMergeRequest struct{ Revision, Identity string }

// Recheck after waiting for the Team mutex and the shared merge slot. An old
// queued verdict must not merge an account that recovered or was reassigned.
func (s *Server) checkProQualityMerge(ctx context.Context, p model.MailAccountProfile, begin bool) error {
	request, _ := ctx.Value(proQualityMergeKey{}).(*proQualityMergeRequest)
	if request == nil {
		return nil
	}
	latest, _, err := s.store.MailAccountCredential(p.Email)
	if err != nil {
		return err
	}
	p = latest
	q, err := s.store.ProQualitySettings()
	if err != nil {
		return err
	}
	c, password, _, err := s.store.ProSettings()
	if err != nil {
		return err
	}
	p.ProWorkflowRunning = false
	if !q.Enabled || q.Action != "merge" || q.Revision != request.Revision || !proQualityConnection(q, c) ||
		!proQualityEligible(p, c) || proQualityIdentity(p, c) != request.Identity || !proQualityCanAct(p) {
		return errors.New("降智合并已取消：配置、账号绑定或运行状态已变化")
	}
	if !begin || p.ProQuality.ActionStarted {
		return nil
	}
	snapshot, err := s.sub2.QualityResults(ctx, c.Sub2, password, []int64{p.Sub2AccountID})
	if err != nil {
		return err
	}
	if err = proQualityClockError(snapshot, time.Now()); err != nil {
		return err
	}
	bad, _ := remoteQualityDecisionSince(q.DecisionSettings(), proQualitySince(p), snapshot, snapshot.Accounts[0])
	if !bad || !s.proQualityPolicyCurrent(q, c) {
		return errors.New("最新结果未确认降智或配置已变化，取消本次自动合并")
	}
	_, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) { p.ProQuality.ActionStarted = true; p.ProMergeTrigger = "quality" })
	return err
}

func (s *Server) finishProQualityMerge(email string, request *proQualityMergeRequest, runErr error) {
	if request == nil {
		return
	}
	_, _ = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		if p.ProQuality.Identity != request.Identity {
			return
		}
		if runErr != nil {
			p.ProQuality.ActionStatus, p.ProQuality.ActionError = "failed", runErr.Error()
		} else {
			p.ProQuality.ActionStatus, p.ProQuality.ActionError = "completed", ""
		}
	})
	status, message := "completed", "降智触发的空间合并已完成"
	if runErr != nil {
		status, message = "failed", "降智合并失败："+runErr.Error()
	}
	if p, _, err := s.store.MailAccountCredential(email); err == nil {
		s.auditProEvent(p, "quality_action", status, "sub2", message, nil)
	}
}
