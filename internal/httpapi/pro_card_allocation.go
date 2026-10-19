package httpapi

import (
	"context"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
)

func (s *Server) restockCardSettings(ctx context.Context, cfg model.ProSettings) (model.ProSettings, bool, error) {
	id, _ := ctx.Value(proRestockContextKey{}).(string)
	if id == "" {
		return cfg, false, nil
	}
	task, err := s.store.ProRestockDispatchTask(id)
	if err != nil {
		return cfg, false, err
	}
	cfg.CardProvider = task.CardProvider
	return cfg, task.StrictCardProvider, nil
}

// Preserve oldest-first order and keep scheduled/restock allocations on the
// chosen issuer even when buying new cards is disabled. Other manual cards
// retain their historical compatibility with the default EJH selection.
func (s *Server) automaticProCards(cfg model.ProSettings, strictProvider ...bool) ([]gptpay.Card, error) {
	cards, _, err := s.store.GPTPayCards(int(^uint(0)>>1), 0, true)
	if err != nil {
		return nil, err
	}
	filtered := make([]gptpay.Card, 0, len(cards))
	for _, card := range cards {
		if store.RestockCardMatches(card, cfg.AutoCardProvider(), len(strictProvider) > 0 && strictProvider[0]) {
			filtered = append(filtered, card)
		}
	}
	return filtered, nil
}

// Serialize selection, optional issuance and reservation across manual, auto
// and scheduled starts. The database still enforces physical-card occupancy.
func (s *Server) reserveProStartCard(ctx context.Context, email, selected string, autoOpen, enforceMaximum bool, admissionChecks ...func() error) (gptpay.Card, gptpay.CardSecret, error) {
	s.proCardAdmissionMu.Lock()
	defer s.proCardAdmissionMu.Unlock()
	var card gptpay.Card
	var secret gptpay.CardSecret
	for _, check := range admissionChecks {
		if err := check(); err != nil {
			return card, secret, err
		}
	}
	taskID, _ := ctx.Value(proRestockContextKey{}).(string)
	var taskProvider string
	var strictProvider bool
	if taskID != "" {
		task, err := s.store.ProRestockDispatchTask(taskID)
		if err != nil {
			return card, secret, err
		}
		taskProvider, strictProvider = task.CardProvider, task.StrictCardProvider
	} else {
		cfg, _, _, err := s.store.ProSettings()
		if err != nil {
			return card, secret, err
		}
		taskProvider = cfg.AutoCardProvider()
	}
	if profile, _, e := s.store.MailAccountCredential(email); e != nil {
		return card, secret, e
	} else if profile.ProRestockTaskID != "" && profile.ProRestockTaskID != taskID {
		return card, secret, fmt.Errorf("账号已由 Pro 自动补货领取，未购买新卡")
	} else if (taskID != "" || enforceMaximum) && profile.ProAuto.StopRequested {
		return card, secret, fmt.Errorf("用户已停止该账号流程，未自动购买新卡")
	}
	if err := ctx.Err(); err != nil {
		return card, secret, err
	}
	if enforceMaximum {
		pro, _, _, err := s.store.ProSettings()
		if err != nil {
			return card, secret, err
		}
		capacity, err := s.store.ProCapacity()
		if err != nil {
			return card, secret, err
		}
		if capacity.OpenedUnmerged+capacity.Pending >= pro.MaxUnmerged {
			return card, secret, fmt.Errorf("已开通和在途账号已达到最大未合并数")
		}
	}
	var err error
	if selected != "" {
		card, secret, err = s.store.GPTPayCard(selected)
		if err != nil || !card.Enabled {
			return card, secret, fmt.Errorf("请选择已启用的银行卡")
		}
		if !store.RestockCardMatches(card, taskProvider, strictProvider) {
			return card, secret, fmt.Errorf("银行卡不属于本次开通配置的卡台，请刷新后重新选择")
		}
		if card.IssuerDisabled {
			return card, secret, fmt.Errorf("该银行卡所属卡台已停用，请开启卡台后再开通 Pro")
		}
		if err = gptpay.ValidateCard(secret, time.Now()); err != nil {
			return card, secret, err
		}
		if card.Source == "nexovcc" && (card.IssuerLocked || card.IssuerStatus != "1") {
			return card, secret, fmt.Errorf("环球卡片未激活、冻结或状态不可用")
		}
		if card.Source == "ejh" && (card.IssuerLocked || (card.IssuerStatus != "1" && card.IssuerStatus != "3")) {
			return card, secret, fmt.Errorf("卡台卡片未激活、冻结或状态不可用")
		}
		if card.IssuerEnd != "" {
			end, e := time.ParseInLocation("2006-01-02 15:04:05", card.IssuerEnd, time.FixedZone("CST", 8*3600))
			if e != nil || !end.After(time.Now()) {
				return card, secret, fmt.Errorf("卡台卡片已过期或到期时间无效")
			}
		}
		if !card.Available {
			// A previously selectable EJH card may have been claimed by another
			// task. Only this capacity/occupancy case permits automatic fallback;
			// frozen, expired and disabled cards are not silently substituted.
			if autoOpen && (card.CardType == "ejh" || card.CardType == "nexovcc") && (card.RemainingAccounts == 0 || (card.SingleActiveAccount && card.ActiveAccounts > 0)) {
				selected = ""
			} else if card.SingleActiveAccount && card.ActiveAccounts > 0 {
				return card, secret, fmt.Errorf("银行卡正在被 %s 占用（一卡同时一个号）；请启用自动补卡或等待回款确认到账", card.ActiveEmail)
			} else {
				return card, secret, fmt.Errorf("银行卡没有剩余开通名额或状态不可用")
			}
		}
	}
	if selected == "" {
		if !autoOpen {
			return card, secret, fmt.Errorf("请选择银行卡，或启用自动补卡")
		}
		card, err = s.ensureProviderCard(ctx, admissionChecks...)
		if err != nil {
			return card, secret, err
		}
		card, secret, err = s.store.GPTPayCard(card.ID)
		if err != nil {
			return card, secret, err
		}
		if !store.RestockCardMatches(card, taskProvider, strictProvider) {
			return card, secret, fmt.Errorf("开通卡台配置已变化，请刷新后重新选择")
		}
	}
	if err = ctx.Err(); err != nil {
		return card, secret, err
	}
	err = s.store.ReserveProCard(email, card.ID, enforceMaximum, taskID)
	return card, secret, err
}
