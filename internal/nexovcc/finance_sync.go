package nexovcc

import "time"

// Card snapshots replace only that card. Account snapshots replace only their
// requested time window; neither may erase another card's historical evidence.
func MergeCardFinance(old, fresh Finance, serial string) Finance {
	out := old
	out.Trades = []Trade{}
	out.Funds = []Fund{}
	for _, r := range old.Trades {
		if r.Serial != serial {
			out.Trades = append(out.Trades, r)
		}
	}
	for _, r := range old.Funds {
		if r.Serial != serial {
			out.Funds = append(out.Funds, r)
		}
	}
	out.Trades = append(out.Trades, fresh.Trades...)
	out.Funds = append(out.Funds, fresh.Funds...)
	out.CardAt = map[string]time.Time{}
	for k, v := range old.CardAt {
		out.CardAt[k] = v
	}
	out.CardAt[serial] = fresh.At
	out.At = fresh.At
	return out
}

func MergeAccountFinance(old, fresh Finance, since time.Time) Finance {
	out := old
	out.Recharges = []Recharge{}
	out.Funds = []Fund{}
	out.Days = []Daily{}
	rechargeIDs := map[string]bool{}
	for _, r := range fresh.Recharges {
		rechargeIDs[r.ID] = true
	}
	for _, r := range old.Recharges {
		if !rechargeIDs[r.ID] {
			out.Recharges = append(out.Recharges, r)
		}
	}
	out.Recharges = append(out.Recharges, fresh.Recharges...)
	for _, r := range old.Funds {
		if !since.IsZero() && r.At.Before(since) {
			out.Funds = append(out.Funds, r)
		}
	}
	out.Funds = append(out.Funds, fresh.Funds...)
	days := map[string]bool{}
	for _, r := range fresh.Days {
		days[r.Day+"|"+r.Currency] = true
	}
	for _, r := range old.Days {
		if !days[r.Day+"|"+r.Currency] {
			out.Days = append(out.Days, r)
		}
	}
	out.Days = append(out.Days, fresh.Days...)
	out.AccountAt, out.At = fresh.At, fresh.At
	return out
}

func MergeAccountSummary(old, fresh Finance, since time.Time) Finance {
	out := MergeAccountFinance(old, fresh, since)
	// No fund snapshot was requested; preserve all cached card and account fees.
	out.Funds = old.Funds
	return out
}

func MergeCardFunds(old, fresh Finance, serial string) Finance {
	out := old
	out.Funds = []Fund{}
	for _, r := range old.Funds {
		if r.Serial != serial {
			out.Funds = append(out.Funds, r)
		}
	}
	out.Funds = append(out.Funds, fresh.Funds...)
	out.FundsAt = map[string]time.Time{}
	for k, v := range old.FundsAt {
		out.FundsAt[k] = v
	}
	out.FundsAt[serial] = fresh.At
	// Preserve At/CardAt: fetching a receipt did not refresh any trades.
	return out
}

func (f Finance) FundsSyncedAt(serial string) time.Time {
	at := f.CardSyncedAt(serial)
	if f.FundsAt[serial].After(at) {
		at = f.FundsAt[serial]
	}
	return at
}

// A legacy whole-account snapshot was complete for every card. Once scoped
// syncing starts, freshness must come from the exact card, never a different one.
func (f Finance) CardSyncedAt(serial string) time.Time {
	if f.CardAt == nil {
		return f.At
	}
	return f.CardAt[serial]
}

// Deleted alone is insufficient: require the issuance debit, final principal
// return (or proven zero balance), and completed, attributed refunds for every payment.
// A manual expense classification cannot turn an outstanding payment into a refund.
func ClosedFinanceComplete(card Card, f Finance, groups []TradeGroup, zeroBalanceClosed bool) bool {
	if card.Status != "4" || f.CardSyncedAt(card.Serial).IsZero() {
		return false
	}
	var funded, returned, loss int64
	issued, closed := false, zeroBalanceClosed
	for _, r := range f.Funds {
		if r.Serial != card.Serial {
			continue
		}
		n, err := SignedCents(r.Amount)
		if err != nil || r.Currency != "USD" || r.At.IsZero() {
			return false
		}
		switch r.Kind {
		case 3001, 4001:
			if n > 0 {
				return false
			}
			funded -= n
			issued = issued || r.Kind == 3001
		case 3002, 4002:
			if n < 0 {
				return false
			}
			returned += n
			closed = closed || r.Kind == 3002 && n > 0
		default:
			if !IsFee(r.Kind) {
				return false
			}
		}
	}
	for _, g := range groups {
		if g.Serial != card.Serial {
			continue
		}
		if !g.Valid || g.OtherExpense != nil || (g.Payment != "0.00" && !g.ReturnResolved()) {
			return false
		}
		n, err := SignedCents(g.Difference)
		if err != nil {
			return false
		}
		loss += n
	}
	// Preserve unresolved principal; at most one cent of issuer rounding.
	delta := funded - returned - loss
	return issued && closed && delta >= -1 && delta <= 1
}
