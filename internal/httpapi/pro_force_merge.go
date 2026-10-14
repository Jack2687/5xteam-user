package httpapi

import (
	"chapt-space-user/internal/model"
	"chapt-space-user/internal/store"
	"context"
	"sync"
	"time"
)

func ejhCardID(order string) string { return store.EJHID("card", order) }
func forceMergeDue(p model.MailAccountProfile, c model.ProSettings, now time.Time) bool {
	if !c.ForceMergeEnabled || c.ForceMergeMinutes < 1 || p.ManagementScope != "pro" || p.ProPushStartedAt == nil || p.PushStatus != "completed" || p.ProWorkflowRunning || p.ProAuto.Status == "stopped" || (p.ProMigration != nil && p.ProMigration.Paused) || proTransferNeedsConfirmation(p) {
		return false
	}
	if p.TransferStatus == "completed" && p.RemoveStatus == "completed" {
		return false
	}
	if p.ProForceMergeAttempts > c.RetryCount {
		return false
	}
	if p.ProForceMergeNextAt != nil && now.Before(*p.ProForceMergeNextAt) {
		return false
	}
	return !now.Before(p.ProPushStartedAt.Add(time.Duration(c.ForceMergeMinutes) * time.Minute))
}
func (s *Server) tickProForceMerge(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	cfg, _, _, e := s.store.ProSettings()
	if e != nil || !cfg.ForceMergeEnabled {
		return
	}
	now := time.Now()
	for _, p := range s.store.MailAccountsByManagementScope("pro") {
		if !forceMergeDue(p, cfg, now) {
			continue
		}
		if p.ProAuto.ID != "" {
			if p.ProAuto.Status != "waiting_quota" {
				continue
			}
			_, e = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) { next := time.Now(); p.ProAuto.NextCheckAt = &next })
			if e == nil {
				s.queueDueProAutomation(p.Email)
			}
			continue
		}
		v, _ := s.proLocks.LoadOrStore("account:"+p.Email, &sync.Mutex{})
		lock := v.(*sync.Mutex)
		if !lock.TryLock() {
			continue
		}
		latest, _, e := s.store.MailAccountCredential(p.Email)
		if e != nil || !forceMergeDue(latest, cfg, time.Now()) {
			lock.Unlock()
			continue
		}
		_, e = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
			p.ProForceMergeAttempts++
			p.ProMergeTrigger = "push_timeout"
			next := time.Now().Add(time.Duration(max(30, cfg.RetryIntervalSeconds)) * time.Second)
			p.ProForceMergeNextAt = &next
		})
		if e != nil {
			lock.Unlock()
			continue
		}
		if _, e = s.startProMergeJob(p.Email, lock.Unlock); e != nil {
			lock.Unlock()
		} else {
			s.auditProEvent(latest, "space_merge", "pending", cfg.Provider, "推送等待超时，跳过额度等待并执行四步", map[string]any{"minutes": cfg.ForceMergeMinutes})
		}
	}
}
