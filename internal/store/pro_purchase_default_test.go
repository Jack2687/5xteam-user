package store

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"slices"
	"testing"
	"time"
)

func TestProPurchaseDefaultPinsPlanTemplateAndFallback(t *testing.T) {
	for _, provider := range []string{"ejh", "nexovcc"} {
		for _, mapping := range []bool{false, true} {
			p := model.DefaultProSettings()
			p.CardProvider, p.BalanceFallbackEnabled = provider, true
			p.EJHTemplateID, p.NexovccTemplateID = "ejh-default", "global-default"
			p.Sub2.GroupIDs = []int64{3, 1}
			if mapping {
				p.EJHPlanTemplates = map[string]string{"pro20": "ejh20"}
				p.NexovccPlanTemplates = map[string]string{"pro20": "global20"}
			}
			pay := gptpay.Settings{Provider: "cmsnav", PlanCode: "pro20", Country: "PH"}
			got, payment, err := ResolveProPurchase(p, pay, restock.DefaultConfig(), time.Now())
			expected := p.EJHTemplateID
			if provider == "nexovcc" {
				expected = p.NexovccTemplateID
			}
			if mapping {
				expected = p.PlanTemplate(provider, "pro20")
			}
			r := got.PurchaseRoute
			if err != nil || r == nil || r.TemplateID != expected || r.PlanCode != "pro20" || r.CardProvider != provider || !r.BalanceFallbackEnabled || !slices.Equal(r.Sub2GroupIDs, []int64{1, 3}) || payment != pay {
				t.Fatalf("wrong default intent: %+v %v", r, err)
			}
			if p.PurchaseRoute != nil {
				t.Fatal("mutated configuration")
			}
		}
	}
}
