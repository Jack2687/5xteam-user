package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"context"
	"errors"
	"sync"
	"time"
)

var errProAccountDead = errors.New("上游确认账号已停用或删除，已归入死号，停止该账号开通和最后一步")

// Use explicit upstream account evidence. A generic 401, administrative API
// login failure or a proxy/network error is not account deactivation.
func (s *Server) markProDead(email, source, message string) (model.MailAccountProfile, error) {
	p, _, err := s.store.MailAccountCredential(email)
	if err != nil {
		return p, err
	}
	if !p.IsProDead() && !p.CanMarkProDead() {
		return p, nil
	}
	message = redactSensitiveText(message)
	if r := []rune(message); len(r) > 1000 {
		message = string(r[:1000])
	}
	first := !p.IsProDead()
	p, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		if !p.IsProDead() && p.CanMarkProDead() {
			p.ProDead = &model.ProDeadState{At: time.Now(), Source: source, Reason: message}
			p.ChatGPTStatus = "dead"
			p.ChatGPTStatusMessage = message
			p.ChatGPTStatusAt = &p.ProDead.At
			p.RegistrationStatus = "dead"
			p.ProAuto.StopRequested = true
			p.ProAuto.StopVersion++
			p.ProQuality.ActionStatus = "cancelled"
		}
	})
	if err != nil || !p.IsProDead() {
		return p, err
	}
	if first {
		s.auditProEvent(p, "dead", "dead", source, "上游明确确认死号，禁用关联卡片开通并按配置处理补货", map[string]any{"reason": message})
	}
	c, _, _, e := s.proAccountPushSettings(p)
	if e == nil && c.Provider == "sub2" && p.Sub2AccountID > 0 {
		// The configured destination is shared with post-merge routing, but no
		// merge/deletion flags or refund evidence are manufactured.
		settings, _, _, se := s.store.ProSettings()
		if se == nil {
			c.PostMergeGroupIDs = settings.PostMergeGroupIDs
		}
		p, e = s.prepareProPostMergeGroups(p, c)
		if e == nil && p.ProPostMergeGroups != nil {
			p, e = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProPostMergeGroups.Action = "dead" })
		}
	}
	return p, e
}

func (s *Server) startProDeadWorker(ctx context.Context) {
	s.proAutoWG.Add(1)
	go func() {
		defer s.proAutoWG.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				items, err := s.store.ProDeadPending()
				if err != nil {
					continue
				}
				for _, initial := range items {
					if ctx.Err() != nil {
						return
					}
					value, _ := s.proLocks.LoadOrStore("account:"+initial.Email, &sync.Mutex{})
					lock := value.(*sync.Mutex)
					if !lock.TryLock() {
						continue
					}
					func() {
						defer lock.Unlock()
						if s.proAutomationRunning(initial.Email) || s.proLoginAlreadyRunning(initial.Email) {
							return
						}
						p, err := s.markProDead(initial.Email, initial.ProDead.Source, initial.ProDead.Reason)
						if err == nil && p.ProPostMergeGroupsPending() {
							step, cancel := context.WithTimeout(ctx, 60*time.Second)
							defer cancel()
							_, _ = s.finishProPostMergeGroups(step, p)
						}
					}()
				}
			}
		}
	}()
}
