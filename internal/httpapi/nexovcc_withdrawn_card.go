package httpapi

import (
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// A confirmed withdrawal down to the reserve ends routine polling of this
// card. Use persisted evidence, not a low balance alone: a spent card may still
// be waiting for its first refund. Explicit manual refreshes bypass this rule.
func nexovccWithdrawnToReserve(card nexovcc.Card, ops []nexovcc.Operation) bool {
	balance, err := nexovcc.Cents(card.Balance)
	if err != nil || balance > 100 || card.BalanceAt.IsZero() {
		return false
	}
	var withdrawnAt, fundedAt time.Time
	for _, op := range ops {
		if op.CardID != card.ID {
			continue
		}
		if !op.Terminal() {
			return false // Pending money/close operations must still be reconciled.
		}
		if op.Status != "succeeded" || op.Serial != card.Serial {
			continue
		}
		if op.Action == "deposit" && op.CreatedAt.After(fundedAt) {
			fundedAt = op.CreatedAt
		}
		if op.Action != "withdraw_all" && op.Action != "withdraw" {
			continue
		}
		before, beforeErr := nexovcc.Cents(op.BeforeBalance)
		amount, amountErr := nexovcc.Cents(op.Amount)
		if beforeErr == nil && amountErr == nil && amount > 0 && before-amount == 100 &&
			!card.BalanceAt.Before(op.CreatedAt) && op.CreatedAt.After(withdrawnAt) {
			withdrawnAt = op.CreatedAt
		}
	}
	return !withdrawnAt.IsZero() && fundedAt.Before(withdrawnAt)
}
