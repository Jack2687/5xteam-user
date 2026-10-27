package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
	"context"
	"errors"
	"fmt"
	"strings"
)

var errIssuerInsufficientBalance = errors.New("卡台开卡余额不足")

type proBalanceFallbackKey struct{}
type proPaymentConfigKey struct{}

// HTTP errors and uncertain responses never authorize another money request.
// The reserve message is a business rejection even though its JSON code is 500.
func nexovccBalanceRejected(err error) bool {
	var rejected *nexovcc.Error
	if !errors.As(err, &rejected) || rejected.Unknown {
		return false
	}
	message := strings.ToLower(rejected.Message)
	return strings.Contains(message, "余额不足") ||
		strings.Contains(message, "insufficient balance") ||
		(strings.Contains(message, "账户余额强制预留") && strings.Contains(message, "请先充值"))
}

// Only an explicit pre-issue balance failure or a durably recorded definite
// issuer rejection reaches this boundary. Never switch after an unknown issue.
func (s *Server) ensureProviderCardWithFallback(ctx context.Context, checks ...func() error) (gptpay.Card, context.Context, error) {
	card, err := s.ensureProviderCard(ctx, checks...)
	if !errors.Is(err, errIssuerInsufficientBalance) || ctx.Err() != nil {
		return card, ctx, err
	}
	cfg, _, _, e := s.store.ProSettings()
	if e != nil {
		return card, ctx, e
	}
	selected, _, e := s.restockCardSettings(ctx, cfg)
	if e != nil {
		return card, ctx, e
	}
	enabled, other, template := selected.BalanceFallback()
	if !enabled {
		return card, ctx, err
	}
	if other == selected.AutoCardProvider() {
		return card, ctx, fmt.Errorf("%w；备用卡台与当前卡台相同，请检查时段设置", err)
	}
	if other == "ejh" && !cfg.EJHAutoOpen || other == "nexovcc" && !cfg.NexovccAutoOpen {
		return card, ctx, fmt.Errorf("%w；备用卡台自动开卡未开启", err)
	}
	route := selected.PurchaseRoute
	if route == nil {
		pay, ok := ctx.Value(proPaymentConfigKey{}).(gptpay.Settings)
		var pe error
		if !ok {
			pay, _, pe = s.store.GPTPaySettings()
		}
		if pe != nil {
			return card, ctx, pe
		}
		route = &model.ProPurchaseRoute{CardProvider: selected.AutoCardProvider(), PlanCode: pay.PlanCode, PaymentProvider: pay.Provider, Country: pay.Country, CreditQuantity: pay.CreditQuantity}
	}
	next := *route
	next.CardProvider = other
	next.TemplateID = template
	if next.TemplateID == "" {
		next.TemplateID = cfg.PlanTemplate(other, next.PlanCode)
	}
	if e = s.validatePurchaseTemplate(&next); e != nil {
		return card, ctx, fmt.Errorf("%w；备用配置无效：%v", err, e)
	}
	for _, check := range checks {
		if e = check(); e != nil {
			return card, ctx, e
		}
	}
	if id, _ := ctx.Value(proRestockContextKey{}).(string); id != "" {
		if e = s.store.SwitchRestockCardForBalance(id, selected.AutoCardProvider(), next); e != nil {
			return card, ctx, e
		}
	}
	ctx = context.WithValue(ctx, proBalanceFallbackKey{}, &next)
	card, e = s.ensureProviderCard(ctx, checks...)
	if e != nil {
		return card, ctx, fmt.Errorf("%s 余额不足，切换 %s 后：%w", restock.CardProviderName(selected.AutoCardProvider()), restock.CardProviderName(other), e)
	}
	return card, ctx, nil
}
