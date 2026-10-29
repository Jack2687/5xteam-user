package model

// A bounded chain belongs to one full-auto intent. Failed accounts and orders
// remain intact; each successor starts a new login and payment identity.
type ProRechargeReplacement struct {
	RootEmail     string            `json:"root_email"`
	PreviousEmail string            `json:"previous_email,omitempty"`
	NextEmail     string            `json:"next_email,omitempty"`
	Attempts      int               `json:"attempts"`
	Limit         int               `json:"limit"`
	Pending       bool              `json:"pending,omitempty"`
	Queued        bool              `json:"queued,omitempty"`
	Message       string            `json:"message,omitempty"`
	Scheduled     bool              `json:"scheduled,omitempty"`
	CardProvider  string            `json:"card_provider,omitempty"`
	PurchaseRoute *ProPurchaseRoute `json:"purchase_route,omitempty"`
}
