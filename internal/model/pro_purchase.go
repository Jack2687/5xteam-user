package model

import (
	"fmt"
	"slices"
	"time"
)

type ProPurchaseWindow struct {
	BalanceFallbackEnabled  bool    `json:"balance_fallback_enabled"`
	BalanceFallbackProvider string  `json:"balance_fallback_provider,omitempty"`
	Start                   string  `json:"start"`
	End                     string  `json:"end"`
	CardProvider            string  `json:"card_provider"`
	PlanCode                string  `json:"plan_code"`
	ExtraSub2GroupIDs       []int64 `json:"extra_sub2_group_ids,omitempty"`
}

// Non-secret purchase intent, pinned before a workflow starts spending.
type ProPurchaseRoute struct {
	BalanceFallbackEnabled  bool    `json:"balance_fallback_enabled"`
	BalanceFallbackProvider string  `json:"balance_fallback_provider,omitempty"`
	BalanceFallbackTemplate string  `json:"balance_fallback_template,omitempty"`
	CardProvider            string  `json:"card_provider"`
	TemplateID              string  `json:"template_id"`
	PlanCode                string  `json:"plan_code"`
	PaymentProvider         string  `json:"payment_provider"`
	Country                 string  `json:"country"`
	CreditQuantity          int     `json:"credit_quantity"`
	Sub2GroupIDs            []int64 `json:"sub2_group_ids,omitempty"`
}

func purchaseMinute(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil || t.Format("15:04") != s {
		return 0, fmt.Errorf("时间须为 HH:mm")
	}
	return t.Hour()*60 + t.Minute(), nil
}
func (w ProPurchaseWindow) contains(m int) bool {
	a, ea := purchaseMinute(w.Start)
	b, eb := purchaseMinute(w.End)
	if ea != nil || eb != nil || a == b {
		return false
	}
	if a < b {
		return m >= a && m < b
	}
	return m >= a || m < b
}
func (p ProSettings) ValidatePurchaseWindows() error {
	if len(p.PurchaseWindows) > 32 {
		return fmt.Errorf("分时开通最多 32 个时段")
	}
	seen := [1440]bool{}
	for i, w := range p.PurchaseWindows {
		if w.BalanceFallbackEnabled && w.BalanceFallbackProvider != "" && (w.BalanceFallbackProvider == w.CardProvider || (w.BalanceFallbackProvider != "ejh" && w.BalanceFallbackProvider != "nexovcc")) {
			return fmt.Errorf("第 %d 个时段的备用卡台须与主卡台不同", i+1)
		}
		_, a := purchaseMinute(w.Start)
		_, b := purchaseMinute(w.End)
		if a != nil || b != nil || w.Start == w.End {
			return fmt.Errorf("分时开通第 %d 条时间无效，开始与结束不能相同", i+1)
		}
		if (w.CardProvider != "ejh" && w.CardProvider != "nexovcc") || (w.PlanCode != "pro5" && w.PlanCode != "pro20") {
			return fmt.Errorf("分时开通请选择卡台及 5x / 20x 套餐")
		}
		for _, id := range w.ExtraSub2GroupIDs {
			if id <= 0 {
				return fmt.Errorf("分时开通第 %d 条额外推送分组无效", i+1)
			}
		}
		for m := range seen {
			if w.contains(m) {
				if seen[m] {
					return fmt.Errorf("分时开通时段不能重叠（含跨天部分）")
				}
				seen[m] = true
			}
		}
	}
	return nil
}

// Keep the monitoring binding unchanged; these groups are applied only when
// pushing the account, after the launch-time choice has been persisted.
func PurchaseSub2Groups(base, extra []int64) []int64 {
	ids := append(slices.Clone(base), extra...)
	slices.Sort(ids)
	return slices.Compact(ids)
}
func (p ProSettings) PurchaseWindow(now time.Time) (ProPurchaseWindow, bool) {
	if p.PurchaseScheduleEnabled {
		now = now.In(time.FixedZone("Asia/Shanghai", 8*3600))
		for _, w := range p.PurchaseWindows {
			if w.contains(now.Hour()*60 + now.Minute()) {
				return w, true
			}
		}
	}
	return ProPurchaseWindow{}, false
}
func (p ProSettings) PlanTemplate(provider, plan string) string {
	if provider == "nexovcc" {
		return p.NexovccPlanTemplates[plan]
	}
	return p.EJHPlanTemplates[plan]
}

func (p ProSettings) BalanceFallback() (bool, string, string) {
	other := "ejh"
	if p.AutoCardProvider() == "ejh" {
		other = "nexovcc"
	}
	if r := p.PurchaseRoute; r != nil {
		if r.BalanceFallbackProvider != "" {
			other = r.BalanceFallbackProvider
		}
		return r.BalanceFallbackEnabled, other, r.BalanceFallbackTemplate
	}
	return p.BalanceFallbackEnabled, other, ""
}
func (p ProSettings) WithPurchaseRoute(r *ProPurchaseRoute) ProSettings {
	if r == nil {
		return p
	}
	p.CardProvider, p.PurchaseRoute = r.CardProvider, r
	if r.CardProvider == "nexovcc" {
		p.NexovccTemplateID = r.TemplateID
	} else {
		p.EJHTemplateID = r.TemplateID
	}
	return p
}
