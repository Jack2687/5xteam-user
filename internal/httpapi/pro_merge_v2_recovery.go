package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/workflow"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type mergeV2PlanKey struct{}
type mergeV2PlanOptions struct{ TeamID, UserID, PersonalID string }

func (s *Server) performProMergeV2(ctx context.Context, email string) (out model.MailAccountProfile, runErr error) {
	p, _, err := s.store.MailAccountCredential(email)
	if err != nil {
		return p, err
	}
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		return p, err
	}
	rule := cfg.MergeV2RecoveryConfig()
	if p.MergeV2Completed() {
		return p, nil
	}
	if p.ProAuto.StopRequested || ctx.Err() != nil {
		return p, context.Canceled
	}
	// All retries are separate persisted jobs, so waits hold no mother/worker slot.
	defer func() {
		latest, _, e := s.store.MailAccountCredential(email)
		if e != nil {
			runErr = errors.Join(runErr, e)
			return
		}
		m := latest.ProMergeV2
		if m == nil || m.CompletedAt != nil {
			return
		}
		unknown := m.SubmittedAt != nil && !latest.MergeV2Resolved()
		if !unknown && m.Recovery == nil {
			return
		}
		out, e = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			m := p.ProMergeV2
			if m.Recovery == nil {
				m.Recovery = &model.ProMergeV2Recovery{Phase: "removing", Cycles: 1}
			}
			r := m.Recovery
			if r.OriginalError == "" {
				r.OriginalError = redactSensitiveText(oauthErrorText(nil, runErr))
			}
			if runErr != nil {
				r.Error = redactSensitiveText(runErr.Error())
			}
			if p.MergeV2Resolved() && r.Phase != "tail" {
				r.Phase = "tail"
				r.Attempts = 0
			}
			if r.NextAt != nil || r.Exhausted {
				return
			}
			if r.Attempts >= rule.MaxAttempts {
				r.Exhausted = true
				m.Error = "自动恢复次数已用尽，请核对恢复记录后人工处理"
				m.Message = m.Error
				return
			}
			// A stopped job keeps its phase but never silently resumes.
			if !rule.Enabled || p.ProAuto.StopRequested {
				return
			}
			next := time.Now().Add(time.Duration(rule.RetrySeconds) * time.Second)
			r.NextAt = &next
			m.Message = fmt.Sprintf("自动恢复第%d/%d轮，等待%d秒后继续（本轮%d/%d次）", r.Cycles, rule.MaxCycles, rule.RetrySeconds, r.Attempts, rule.MaxAttempts)
		})
		runErr = errors.Join(runErr, e)
		if e == nil && out.ProMergeV2.Recovery != nil {
			r := out.ProMergeV2.Recovery
			s.auditProEvent(out, "merge_v2_recovery", "waiting", "", out.ProMergeV2.Message, map[string]any{"phase": r.Phase, "cycles": r.Cycles, "attempts": r.Attempts, "exhausted": r.Exhausted, "error": r.Error, "next_at": r.NextAt})
		}
	}()
	m := p.ProMergeV2
	if m != nil && m.Recovery != nil {
		r := m.Recovery
		if r.Exhausted {
			return p, errors.New("合并2.0自动恢复次数已用尽，请人工核实")
		}
		if r.NextAt != nil && time.Now().Before(*r.NextAt) {
			return p, errMergeV2Unknown
		}
		if !rule.Enabled && !p.MergeV2Resolved() {
			return p, errors.New("合并2.0自动恢复已关闭")
		}
		if r.Attempts >= rule.MaxAttempts || r.Phase == "reinvite" && r.Cycles >= rule.MaxCycles {
			p, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProMergeV2.Recovery.Exhausted = true
				p.ProMergeV2.Recovery.NextAt = nil
			})
			return p, errors.Join(err, errors.New("合并2.0恢复已达到配置上限"))
		}
		// Claim an attempt durably before any network I/O. A restart cannot reset its budget.
		p, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			r := p.ProMergeV2.Recovery
			r.NextAt = nil
			r.Attempts++
			now := time.Now()
			r.CheckedAt = &now
		})
		if err != nil {
			return p, err
		}
		switch r.Phase {
		case "removing", "checking":
			return s.recoverProMergeV2Plan(ctx, p, rule)
		case "reinvite":
			p, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				m := p.ProMergeV2
				r := m.Recovery
				if m.SubmittedAt != nil {
					r.Submissions = append(r.Submissions, *m.SubmittedAt)
				}
				m.SubmittedAt = nil
				m.InviteAcceptAttemptedAt = nil
				m.InviteAcceptedAt = nil
				m.UserID = ""
				m.Steps = map[string]string{}
				m.Stage = "invite"
				m.Status = "interrupted"
				m.Error = ""
				r.Cycles++
				r.Attempts = 0
				r.Phase = "resubmit"
			})
			if err != nil {
				return p, err
			}
		}
	}
	out, runErr = s.performProMergeV2Attempt(ctx, email)
	// A renewed uncertain attempt must return through removal/verification, not resubmit.
	if out.ProMergeV2 != nil && out.ProMergeV2.Recovery != nil && out.ProMergeV2.SubmittedAt != nil && !out.MergeV2Resolved() {
		out, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			r := p.ProMergeV2.Recovery
			if r.Phase == "resubmit" {
				r.Phase = "removing"
				r.Attempts = 0
			}
		})
		runErr = errors.Join(runErr, err)
	}
	return out, runErr
}

func (s *Server) recoverProMergeV2Plan(ctx context.Context, p model.MailAccountProfile, rule model.ProMergeV2RecoverySettings) (model.MailAccountProfile, error) {
	m := p.ProMergeV2
	if m.SubmittedAt == nil || m.UserID == "" || m.AcceptedAt != nil {
		return p, errors.New("套餐兜底缺少原提交及团队身份检查点")
	}
	// One attempt is bounded independently of the complete job timeout.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	value, _ := s.autoAdminLocks.LoadOrStore(m.TeamID, &sync.Mutex{})
	unlock, err := lockProMutex(ctx, value.(*sync.Mutex))
	if err != nil {
		return p, err
	}
	unlock = sync.OnceFunc(unlock)
	defer unlock()
	release, err := s.proMergeSlot(ctx)
	if err != nil {
		return p, err
	}
	defer release()
	token, err := s.mergeV2AdminAccessToken(ctx, m.AdminID, m.TeamID)
	if err != nil {
		return p, err
	}
	admin, _, err := s.currentAdminCredential(ctx, m.AdminID)
	if err != nil {
		return p, err
	}
	if admin.TeamAccountID != m.TeamID {
		return p, errors.New("原母号团队已变化，禁止在其他团队恢复")
	}
	settings, err := s.settingsForAdmin(s.store.Settings(), admin)
	if err != nil {
		return p, err
	}
	client, err := workflow.NewClient(settings)
	if err != nil {
		return p, err
	}
	member, err := client.FindMemberByEmail(ctx, token, m.TeamID, p.Email)
	if err != nil {
		return p, err
	}
	if member != nil {
		if member.ID != m.UserID || (member.Role != "standard-user" && member.Role != "member" && member.Role != "user") {
			return p, errors.New("原团队成员身份或权限不匹配，禁止移出")
		}
		if err = ctx.Err(); err != nil {
			return p, err
		}
		latest, _, e := s.store.MailAccountCredential(p.Email)
		if e != nil {
			return p, e
		}
		if latest.ProAuto.StopRequested {
			return p, context.Canceled
		}
		_, removeErr := client.Kick(ctx, token, m.TeamID, m.UserID)
		// A lost DELETE response is reconciled by the full member+invite lists.
		if err = client.ConfirmMergeV2Removed(ctx, token, m.TeamID, p.Email, m.UserID); err != nil {
			return p, errors.Join(removeErr, err)
		}
	} else if err = client.ConfirmMergeV2Removed(ctx, token, m.TeamID, p.Email, m.UserID); err != nil {
		return p, err
	}
	p, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		m := p.ProMergeV2
		m.Steps["remove"] = "completed"
		m.Recovery.Phase = "checking"
		m.Message = "已确认移出原团队，重新登录个人空间核验套餐"
	})
	if err != nil {
		return p, err
	}
	s.auditProEvent(p, "merge_v2_recovery", "running", "", p.ProMergeV2.Message, map[string]any{"phase": "checking"})
	unlock() // Personal login must not hold up other work on this mother.
	opts := &mergeV2PlanOptions{TeamID: m.TeamID, UserID: m.UserID, PersonalID: p.OAuthAccountID}
	var result map[string]any
	if s.proMergeV2Plan != nil {
		result, err = s.proMergeV2Plan(ctx, p)
	} else {
		result, err = s.executeOpenAILoginContext(context.WithValue(ctx, mergeV2PlanKey{}, opts), p.Email, "chatgpt_at", nil, func(d protocolOAuthDiagnostic) {
			s.auditMergeV2Login(p, "", "recovery", p.ProMergeV2.Recovery.Attempts, d)
		})
	}
	if err != nil || result["success"] != true {
		return p, fmt.Errorf("个人套餐核验未完成：%s", oauthErrorText(result, err))
	}
	if result["user_id"] != m.UserID {
		return p, errors.New("个人套餐核验用户身份不匹配")
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	latest, _, err := s.store.MailAccountCredential(p.Email)
	if err != nil {
		return p, err
	}
	if latest.ProAuto.StopRequested {
		return p, context.Canceled
	}
	switch result["plan"] {
	case "free":
		p, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
			m := p.ProMergeV2
			m.CompletionBasis = "plan_free"
			m.Recovery.Phase = "tail"
			m.Recovery.Attempts = 0
			m.Error = ""
			m.Message = "个人套餐已核验为Free，按套餐兜底完成；退款仍以流水为准"
		})
		if err != nil {
			return p, err
		}
		s.auditProEvent(p, "merge_v2_recovery", "completed", "", p.ProMergeV2.Message, map[string]any{"completion_basis": "plan_free"})
		release()
		release = func() {} // The ordinary tail acquires its own slot.
		return s.performProMergeV2Attempt(ctx, p.Email)
	case "paid":
		p, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
			m := p.ProMergeV2
			r := m.Recovery
			r.Phase = "reinvite"
			r.Attempts = 0
			m.Message = "个人套餐仍为付费，原账号原单等待重新排队办理"
			if r.Cycles >= rule.MaxCycles {
				r.Exhausted = true
				m.Error = "已达到套餐兜底轮次上限，个人套餐仍为付费；已移出原团队，请人工核实"
				m.Message = m.Error
			}
		})
		return p, errors.Join(err, errors.New("个人套餐仍为付费"))
	default:
		return p, errors.New("个人套餐响应不明确，保留原提交证据")
	}
}

func (s *Server) tickProMergeV2Recovery(ctx context.Context) {
	if ctx.Err() != nil || s.checkProAutoFinalStepAllowed() != nil {
		return
	}
	cfg, _, _, err := s.store.ProSettings()
	if err != nil || !cfg.MergeV2RecoveryConfig().Enabled {
		return
	}
	items, err := s.store.DueProMergeV2Recovery(time.Now())
	if err != nil {
		return
	}
	for _, p := range items {
		if ctx.Err() != nil {
			return
		}
		value, _ := s.proLocks.LoadOrStore("account:"+p.Email, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		if !lock.TryLock() {
			continue
		}
		if p.ProMergeV2.Recovery == nil {
			_, err = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
				next := time.Now().Add(time.Duration(cfg.MergeV2RecoveryConfig().RetrySeconds) * time.Second)
				p.ProMergeV2.Recovery = &model.ProMergeV2Recovery{Phase: "removing", Cycles: 1, NextAt: &next, OriginalError: p.ProMergeV2.Error}
			})
			lock.Unlock()
			continue
		}
		if _, err = s.startProEndJob(p.Email, lock.Unlock, nil, "merge_v2"); err != nil {
			lock.Unlock()
		}
	}
}
