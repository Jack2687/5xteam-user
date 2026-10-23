package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
	"context"
	"sync"
	"time"
)

// Transfer destinations have their own result IDs and credentials. Results are
// displayed without replaying the original site's routing action at this site.
func (s *Server) observeSIWCQuality(ctx context.Context, p model.MailAccountProfile, q model.ProQualitySettings) {
	value, _ := s.proLocks.LoadOrStore("account:"+p.Email, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return
	}
	defer lock.Unlock()
	p, _, e := s.store.MailAccountCredential(p.Email)
	if e != nil || p.ProSIWC == nil || p.ProSIWC.Status != "active" {
		return
	}
	target, secret, _, e := s.proAccountPushSettings(p)
	if e != nil {
		return
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	snapshot, e := s.sub2.QualityResults(call, target.Sub2, secret, []int64{p.Sub2AccountID})
	var result sub2.RemoteQualityResult
	if e == nil {
		e = proQualityClockError(snapshot, time.Now())
		if len(snapshot.Accounts) == 1 {
			result = snapshot.Accounts[0]
		}
	}
	state, _, _ := proQualityObservation(p, q, target, snapshot, result, e)
	state.Action, state.ActionStatus = "siwc", "completed"
	_, _ = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		if p.ProSIWC != nil && p.ProSIWC.Status == "active" {
			p.ProQuality = state
		}
	})
}
