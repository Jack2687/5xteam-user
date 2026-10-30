package model

import "chatgpt-space-merge/internal/gptpay"

// Ten primary replacements plus ten accounts after the one permitted switch.
const ProRechargeChainMaxLinks = 20

// Only non-secret purchase intent is persisted; keys always come from the
// selected supplier's own settings. Global role changes cannot reroute a chain.
type ProRechargePayment struct {
	Provider       string `json:"provider"`
	PlanCode       string `json:"plan_code"`
	Country        string `json:"country"`
	CreditQuantity int    `json:"credit_quantity"`
}

func RechargePayment(pay gptpay.Settings) *ProRechargePayment {
	return &ProRechargePayment{pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity}
}

func (p ProRechargePayment) Apply(pay gptpay.Settings) gptpay.Settings {
	pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity = p.Provider, p.PlanCode, p.Country, p.CreditQuantity
	return pay
}

// A bounded chain belongs to one full-auto intent. Failed accounts and orders
// remain intact; each successor starts a new login and payment identity.
type ProRechargeReplacement struct {
	RootEmail        string              `json:"root_email"`
	PreviousEmail    string              `json:"previous_email,omitempty"`
	NextEmail        string              `json:"next_email,omitempty"`
	Attempts         int                 `json:"attempts"`
	Limit            int                 `json:"limit"`
	Pending          bool                `json:"pending,omitempty"`
	Queued           bool                `json:"queued,omitempty"`
	Message          string              `json:"message,omitempty"`
	Scheduled        bool                `json:"scheduled,omitempty"`
	CardProvider     string              `json:"card_provider,omitempty"`
	PurchaseRoute    *ProPurchaseRoute   `json:"purchase_route,omitempty"`
	PrimaryProvider  string              `json:"primary_provider,omitempty"`
	BackupProvider   string              `json:"backup_provider,omitempty"`
	FailoverLimit    int                 `json:"failover_limit,omitempty"`
	ProviderSwitched bool                `json:"provider_switched,omitempty"`
	Payment          *ProRechargePayment `json:"payment,omitempty"`
}

func (r ProRechargeReplacement) CanContinue() bool {
	return r.Attempts < r.Limit || (r.Payment != nil && r.PrimaryProvider != "" && r.BackupProvider != "" && r.BackupProvider != r.PrimaryProvider && r.FailoverLimit > 0 && r.Attempts < r.Limit+r.FailoverLimit)
}
