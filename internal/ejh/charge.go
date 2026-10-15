package ejh

import "time"

// ActivationCharge deliberately excludes PAN and arbitrary supplier metadata.
// Amounts are distinct supplier fields; authorization and clearing are never summed.
type ActivationCharge struct {
	ID            string    `json:"id"`
	TradeNo       string    `json:"trade_no"`
	Type          string    `json:"type"`
	Status        string    `json:"status"`
	CardAmount    string    `json:"card_amount"`
	CardCurrency  string    `json:"card_currency"`
	TradeAmount   string    `json:"trade_amount"`
	TradeCurrency string    `json:"trade_currency"`
	BillAmount    string    `json:"bill_amount"`
	BillCurrency  string    `json:"bill_currency"`
	Merchant      string    `json:"merchant"`
	TradeTime     string    `json:"trade_time"`
	SyncedAt      time.Time `json:"synced_at"`
}
type ActivationCredits struct {
	Charged    int64     `json:"charged"`
	Reserved   int64     `json:"reserved"`
	Released   int64     `json:"released"`
	Settlement string    `json:"settlement"`
	UpdatedAt  time.Time `json:"updated_at"`
}
