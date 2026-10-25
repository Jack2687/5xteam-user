package ejh

import "time"

// The percentage is a configured estimate, not an upstream fee transaction.
// A pointer distinguishes a locked zero fee from a missing snapshot.
type ReversalFeeRate struct {
	Percent  float64   `json:"percent"`
	LockedAt time.Time `json:"locked_at"`
}

// Apply the same per-order snapshot to card and financial totals as to cases.
func ApplyReversalFeeRates(trades []TradeRecord, cases []Case) {
	rates := map[string]*ReversalFeeRate{}
	for _, c := range cases {
		if c.CardType == "ejh" && c.State == "returned" {
			rates[c.ID] = c.ReversalFee
		}
	}
	for i := range trades {
		trades[i].ReversalFee = rates[trades[i].OrderID]
	}
}
