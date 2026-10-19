package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
	"context"
	"fmt"
	"time"
)

type proPurchaseContextKey struct{}

func applyPurchasePay(pay gptpay.Settings, r *model.ProPurchaseRoute) (gptpay.Settings, error) {
	if r != nil {
		if pay.Provider != r.PaymentProvider {
			return pay, fmt.Errorf("开通供应商已变化，未启动旧任务")
		}
		pay.PlanCode, pay.Country, pay.CreditQuantity = r.PlanCode, r.Country, r.CreditQuantity
	}
	return pay, nil
}
func (s *Server) validatePurchaseTemplate(r *model.ProPurchaseRoute) error {
	if r == nil {
		return nil
	}
	if r.CardProvider == "nexovcc" {
		cfg, _, err := s.store.NexovccSettings()
		if err != nil {
			return err
		}
		for _, t := range cfg.Templates {
			if t.ID == r.TemplateID && t.PlanCode == r.PlanCode {
				return nil
			}
		}
	} else {
		cfg, _, err := s.store.EJHSettings()
		if err != nil {
			return err
		}
		for _, t := range cfg.Templates {
			if t.ID == r.TemplateID && t.PlanCode == r.PlanCode {
				return nil
			}
		}
	}
	return fmt.Errorf("%s 的 %s 开卡模板不存在或适用套餐不匹配，请重新配置", restock.CardProviderName(r.CardProvider), r.PlanCode)
}
func (s *Server) resolveProPurchase(p model.ProSettings, pay gptpay.Settings, c restock.Config, now time.Time) (model.ProSettings, gptpay.Settings, error) {
	p, pay, err := store.ResolveProPurchase(p, pay, c, now)
	if err == nil {
		err = s.validatePurchaseTemplate(p.PurchaseRoute)
	}
	return p, pay, err
}
func (s *Server) validateProPurchaseConfig(p model.ProSettings, pay gptpay.Settings) error {
	if err := p.ValidatePurchaseWindows(); err != nil {
		return err
	}
	for provider, mappings := range map[string]map[string]string{"ejh": p.EJHPlanTemplates, "nexovcc": p.NexovccPlanTemplates} {
		for plan, id := range mappings {
			if id == "" {
				continue
			}
			if plan != "pro5" && plan != "pro20" {
				return fmt.Errorf("模板映射仅支持 5x / 20x")
			}
			if err := s.validatePurchaseTemplate(&model.ProPurchaseRoute{CardProvider: provider, PlanCode: plan, TemplateID: id}); err != nil {
				return err
			}
		}
	}
	if !p.PurchaseScheduleEnabled {
		return nil
	}
	if len(p.PurchaseWindows) == 0 {
		return fmt.Errorf("请至少添加一个分时开通时段")
	}
	for _, w := range p.PurchaseWindows {
		r := &model.ProPurchaseRoute{CardProvider: w.CardProvider, PlanCode: w.PlanCode, TemplateID: p.PlanTemplate(w.CardProvider, w.PlanCode)}
		if err := s.validatePurchaseTemplate(r); err != nil {
			return err
		}
		v := pay
		v.PlanCode, v.CreditQuantity = w.PlanCode, 0
		if _, err := gptpay.NormalizeSettings(v); err != nil {
			return err
		}
		if err := s.validateEJHAutomation(p.WithPurchaseRoute(r)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) purchaseContext(ctx context.Context, p model.ProSettings, pay gptpay.Settings) (context.Context, gptpay.Settings, error) {
	var route *model.ProPurchaseRoute
	if id, _ := ctx.Value(proRestockContextKey{}).(string); id != "" {
		t, err := s.store.ProRestockDispatchTask(id)
		if err != nil {
			return ctx, pay, err
		}
		route = t.PurchaseRoute
	} else if pinned, _ := ctx.Value(proPurchaseContextKey{}).(*model.ProPurchaseRoute); pinned != nil {
		c, err := s.store.ProRestockConfig()
		if err != nil {
			return ctx, pay, err
		}
		if reason := c.NoRestockReason(time.Now()); reason != "" {
			return ctx, pay, fmt.Errorf("%s", reason)
		}
		current, _, err := s.resolveProPurchase(p, pay, c, time.Now())
		if err != nil {
			return ctx, pay, err
		}
		if !store.SameProPurchase(current.PurchaseRoute, pinned) {
			return ctx, pay, fmt.Errorf("分时时段已变化，等待下次定时检查")
		}
		route = pinned
	} else {
		c, err := s.store.ProRestockConfig()
		if err != nil {
			return ctx, pay, err
		}
		if reason := c.NoRestockReason(time.Now()); reason != "" {
			return ctx, pay, fmt.Errorf("%s", reason)
		}
		p, pay, err = s.resolveProPurchase(p, pay, c, time.Now())
		if err != nil {
			return ctx, pay, err
		}
		route = p.PurchaseRoute
	}
	if err := s.validatePurchaseTemplate(route); err != nil {
		return ctx, pay, err
	}
	var err error
	pay, err = applyPurchasePay(pay, route)
	if route != nil {
		ctx = context.WithValue(ctx, proPurchaseContextKey{}, route)
	}
	return ctx, pay, err
}
