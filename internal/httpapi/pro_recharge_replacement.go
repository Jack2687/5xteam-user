package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
)

func proReplacementOwner(p model.MailAccountProfile) string {
	if r := p.ProAuto.Replacement; r != nil {
		return r.RootEmail + "/" + r.PreviousEmail
	}
	return ""
}

func (s *Server) queueProRechargeReplacements(ctx context.Context) {
	v, _ := s.proLocks.LoadOrStore("recharge-replacements", &sync.Mutex{})
	lock := v.(*sync.Mutex)
	if !lock.TryLock() {
		return
	}
	s.proAutoMu.Lock()
	if s.proAutoClosed {
		s.proAutoMu.Unlock()
		lock.Unlock()
		return
	}
	s.proAutoWG.Add(1)
	s.proAutoMu.Unlock()
	go func() { defer s.proAutoWG.Done(); defer lock.Unlock(); s.dispatchProRechargeReplacements(ctx) }()
}

func (s *Server) dispatchProRechargeReplacements(ctx context.Context) {
	items, err := s.store.ProRechargeReplacementWork()
	if err != nil {
		return
	}
	for _, p := range items {
		if ctx.Err() != nil {
			return
		}
		if s.proAutomationRunning(p.Email) {
			continue
		}
		r := p.ProAuto.Replacement
		if p.ProAuto.RetryAt != nil && time.Now().Before(*p.ProAuto.RetryAt) {
			continue
		}
		if !r.Pending && !r.Queued {
			if e := s.store.QueueProRechargeReplacement(p.Email, p.ProAuto.ID); e != nil {
				continue
			}
			var e error
			p, _, e = s.store.MailAccountCredential(p.Email)
			if e != nil {
				continue
			}
			r = p.ProAuto.Replacement
		}
		if r.Pending {
			next, e := s.store.ClaimProRechargeReplacement(p.Email)
			if e != nil {
				s.rechargeReplacementWait(p, e.Error())
				continue
			}
			s.auditProEvent(p, "recharge", "failed", "pro_auto", "第二步失败，换新账号从登录执行", map[string]any{"next_email": next.Email, "replacement_attempt": next.ProAuto.Replacement.Attempts, "replacement_limit": r.Limit})
			p, r = next, next.ProAuto.Replacement
		}
		if !r.Queued || p.ProRestockTaskID != "" {
			continue
		} // restock uses its normal budgeted dispatcher
		cfg, _, _, e := s.store.ProSettings()
		if e != nil {
			continue
		}
		cfg.CardProvider = r.CardProvider
		pay, _, e := s.store.GPTPaySettings()
		if e != nil {
			continue
		}
		workCtx := ctx
		if r.Scheduled {
			workCtx = context.WithValue(workCtx, proScheduledContextKey{}, true)
			workCtx, pay, e = s.purchaseContext(workCtx, cfg, pay)
			if e != nil {
				s.rechargeReplacementWait(p, e.Error())
				continue
			}
			cfg, _, e = s.restockCardSettings(workCtx, cfg)
		} else {
			workCtx, cfg, e = s.manualProPurchase(workCtx, cfg, pay, r.CardProvider, r.PurchaseRoute)
		}
		if e != nil {
			s.rechargeReplacementWait(p, e.Error())
			continue
		}
		var cardID string
		if !cfg.AutoCardEnabled() {
			cards, e := s.automaticProCards(cfg)
			if e != nil {
				s.rechargeReplacementWait(p, e.Error())
				continue
			}
			if len(cards) == 0 {
				s.rechargeReplacementWait(p, "等待可用银行卡，原未决订单及卡占用保留")
				continue
			}
			cardID = cards[0].ID
		}
		body, _ := json.Marshal(map[string]any{"provider": pay.Provider, "plan_code": pay.PlanCode, "country": pay.Country, "credit_quantity": pay.CreditQuantity, "card_provider": cfg.CardProvider, "card_id": cardID})
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(workCtx)
		request.SetPathValue("email", p.Email)
		response := httptest.NewRecorder()
		s.startProAutomation(response, request)
		if response.Code != http.StatusAccepted {
			var reply struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(response.Body.Bytes(), &reply)
			s.rechargeReplacementWait(p, firstNonEmpty(reply.Error, "新账号尚未启动，等待重试准备"))
		}
	}
}

func (s *Server) rechargeReplacementWait(p model.MailAccountProfile, message string) {
	_, _ = s.store.UpdateProAccount(p.Email, func(current *model.MailAccountProfile) {
		if current.ProAuto.ID != p.ProAuto.ID || current.ProAuto.StopRequested || current.ProAuto.Replacement == nil {
			return
		}
		if r := current.ProAuto.Replacement; r.Pending || r.Queued {
			r.Message = message
			next := time.Now().Add(10 * time.Second)
			current.ProAuto.RetryAt = &next
		}
	})
}
