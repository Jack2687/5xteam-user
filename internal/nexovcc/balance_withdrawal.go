package nexovcc

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BalanceWithdrawal records a withdrawal exception, NOT proof of a refund.
// Case matching, card reuse and closing must continue to use actual receipts.
type BalanceWithdrawal struct {
	Cycle         string    `json:"cycle"`
	LedgerBalance string    `json:"ledger_balance"`
	Balance       string    `json:"balance"`
	BalanceAt     time.Time `json:"balance_at"`
}

// Late reversal rows must not create another withdrawal for money that was
// already withdrawn before those rows arrived. New payments/top-ups start a
// new cycle; refund timestamps, polling and our own withdrawals do not.
func WithdrawFundingCycle(card Card, f Finance) string {
	parts := []string{card.ID}
	for _, g := range Groups(f) {
		if g.Serial == card.Serial && g.Payment != "0.00" {
			parts = append(parts, "payment:"+g.ID)
		}
	}
	for _, fund := range f.Funds {
		if fund.Serial == card.Serial && fund.Kind == 4001 {
			parts = append(parts, "deposit:"+fund.ID+":"+fund.Amount)
		}
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%x", h[:16])
}
