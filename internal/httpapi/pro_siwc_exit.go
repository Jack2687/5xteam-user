package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"context"
	"time"
)

// Refresh the source after group removal too: requests already running may
// finish later. Monotonic source/target totals make repeated reads idempotent.
func (s *Server) refreshSIWCSourceCost(ctx context.Context, p model.MailAccountProfile) error {
	if p.ProSIWC == nil || p.ProSIWC.SourceAccountID <= 0 {
		return nil
	}
	v := p.ProSIWC
	src, e := s.store.DownstreamTarget(v.SourceTargetID)
	if e != nil {
		return e
	}
	costs, e := s.sub2.QueryTotalCosts(ctx, src.Sub2, src.Secret, v.SourceAccountID)
	if e != nil {
		return e
	}
	_, e = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		if p.ProSIWC == nil || p.ProSIWC.SourceTargetID != v.SourceTargetID {
			return
		}
		v := p.ProSIWC
		v.CarriedCost = max(v.CarriedCost, costs.StandardCostUSD)
		if costs.UserCostUSD != nil {
			v.CarriedUserCost = max(v.CarriedUserCost, *costs.UserCostUSD)
		}
		p.CostDownstreamSnapshot = v.CarriedCost + v.NewCost
		p.UserCostDownstreamSnapshot = v.CarriedUserCost + v.NewUserCost
		now := time.Now()
		v.SourceCostCheckedAt = &now
	})
	return e
}

// Stop routing before starting an irreversible merge/deletion. On failure leave
// the transfer unresolved so restocking cannot mistake it for lost capacity.
func (s *Server) endSIWCService(ctx context.Context, p model.MailAccountProfile) error {
	settings, password, _, e := s.proAccountPushSettings(p)
	if e != nil {
		return e
	}
	if settings.Sub2.AuthMode != "siwc" && p.ProSIWC == nil {
		return nil
	}
	if p.ProSIWC != nil && p.ProSIWC.Status == "ended" {
		return nil
	}
	if settings.Sub2.AuthMode == "siwc" && p.Sub2AccountID > 0 && (p.ProSIWC == nil || p.ProSIWC.AccountID > 0) {
		if e = s.sub2.SetSchedulable(ctx, settings.Sub2, password, p.Sub2AccountID, false); e != nil {
			return e
		}
		if p.ProSIWC != nil {
			if e = s.sub2.SetAccountGroups(ctx, settings.Sub2, password, p.Sub2AccountID, []int64{}); e != nil {
				return e
			}
		}
		if costs, err := s.sub2.QueryTotalCosts(ctx, settings.Sub2, password, p.Sub2AccountID); err == nil {
			_, _ = s.saveProCostSnapshot(p, settings, costs)
		}
	}
	if p.ProSIWC != nil {
		_ = s.refreshSIWCSourceCost(ctx, p)
		_, e = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
			p.ProSIWC.Status = "ended"
			now := time.Now()
			p.ProSIWC.EndedAt = &now
			p.ProSIWC.RetryAt = nil
			p.ProAuto.NextCheckAt = &now
		})
	}
	return e
}
