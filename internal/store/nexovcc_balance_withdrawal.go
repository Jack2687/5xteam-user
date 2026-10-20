package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Candidate discovery reads only local snapshots. BeginNexovccOperation repeats
// these checks in its admission transaction after the live balance was fetched.
func (s *Store) NexovccBalanceWithdrawal(cardID string) (*nexovcc.BalanceWithdrawal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw, key string
	if err := s.db.QueryRow(`SELECT profile,coalesce(card_key,'') FROM nexovcc_cards WHERE id=?`, cardID).Scan(&raw, &key); err != nil {
		return nil, err
	}
	var card nexovcc.Card
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		return nil, err
	}
	return nexovccBalanceWithdrawal(s.db, card, key)
}

func nexovccWithdrawalFinance(q proCapacityQuery) (nexovcc.Finance, error) {
	var f nexovcc.Finance
	var raw string
	err := q.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &f)
	}
	return f, err
}

func nexovccBalanceWithdrawalUsed(q proCapacityQuery, card nexovcc.Card, f nexovcc.Finance) (bool, error) {
	var used bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_operations WHERE scope=? AND json_extract(profile,'$.balance_withdrawal.cycle')=?)`, "card:"+card.ID, nexovcc.WithdrawFundingCycle(card, f)).Scan(&used)
	return used, err
}

func nexovccBalanceWithdrawal(q nexovccCaseQuery, card nexovcc.Card, key string) (*nexovcc.BalanceWithdrawal, error) {
	if key == "" || !card.Imported || card.Status != "1" || card.Control == "frozen" || card.Error != "" || card.BalanceAt.IsZero() {
		return nil, nil
	}
	balance, err := nexovcc.Cents(card.Balance)
	if err != nil || balance <= 100 {
		return nil, nil
	}
	limit, err := proCardLimit(q, key)
	if err != nil {
		return nil, err
	}
	opened, _, err := proCardCounts(q, key, "")
	if err != nil || opened < limit {
		return nil, err
	}
	var busy bool
	err = q.QueryRow(`SELECT
 EXISTS(SELECT 1 FROM pro_card_reservations WHERE card_key=?) OR
 EXISTS(SELECT 1 FROM pro_card_usage WHERE card_key=? AND status NOT IN ('success','failed')) OR
 EXISTS(SELECT 1 FROM nexovcc_operations WHERE scope=? AND status NOT IN ('succeeded','failed')) OR
 EXISTS(SELECT 1 FROM mail_accounts m WHERE
 (json_extract(m.profile,'$.pro_auto.card_id')=? OR EXISTS(SELECT 1 FROM pro_card_usage u WHERE u.card_key=? AND u.email=m.email COLLATE NOCASE)) AND
 (json_extract(m.profile,'$.pro_auto.status') IN ('running','waiting_quota','awaiting_push') OR
 json_extract(m.profile,'$.pro_workflow_running')=1 OR
 EXISTS(SELECT 1 FROM json_each(json_extract(m.profile,'$.pro_manual_stages')) j WHERE json_extract(j.value,'$.status')='running')))`, key, key, "card:"+card.ID, card.ID, key).Scan(&busy)
	if err != nil || busy {
		return nil, err
	}
	f, err := nexovccWithdrawalFinance(q)
	if err != nil || f.Error != "" {
		return nil, err
	}
	if used, err := nexovccBalanceWithdrawalUsed(q, card, f); err != nil || used {
		return nil, err
	}
	ledgerAt := f.At
	if f.CardAt != nil {
		ledgerAt = f.CardAt[card.Serial]
	}
	if ledgerAt.IsZero() {
		return nil, nil
	}
	// Only original, valid same-card records are evidence. A manual expense or
	// a manually marked "returned" case cannot manufacture a returned balance.
	groups := map[string]nexovcc.TradeGroup{}
	var netPaid int64
	for _, g := range nexovcc.Groups(f) {
		if g.Serial != card.Serial {
			continue
		}
		paid, pe := nexovcc.Cents(g.Payment)
		returned, re := nexovcc.Cents(g.Returned)
		if !g.Valid || pe != nil || re != nil || g.At.IsZero() || g.At.After(card.BalanceAt) || g.ReturnedAt.After(card.BalanceAt) {
			return nil, nil
		}
		var reviewed bool
		if err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_trade_reviews WHERE group_id=? AND coalesce(json_extract(profile,'$.voided'),0)=0)`, g.ID).Scan(&reviewed); err != nil || reviewed {
			return nil, err
		}
		groups[g.ID] = g
		netPaid += paid - returned
	}
	var funding int64
	hasOpening := false
	for _, fund := range f.Funds {
		if fund.Serial != card.Serial {
			continue
		}
		amount, err := nexovcc.SignedCents(fund.Amount)
		if err != nil || fund.Currency != "USD" || fund.At.IsZero() || fund.At.After(card.BalanceAt) {
			return nil, nil
		}
		switch fund.Kind {
		case 3001, 4001: // Funding is a debit from the main account.
			if amount >= 0 {
				return nil, nil
			}
			hasOpening = hasOpening || fund.Kind == 3001
			funding -= amount
		case 4002: // Withdrawal is a credit to the main account.
			if amount < 0 {
				return nil, nil
			}
			funding -= amount
		case 3002: // An active card with a deletion refund needs investigation.
			return nil, nil
		case 4004, 5000:
			// Main-account fees/refunds do not increase the card's funding.
		default:
			if !nexovcc.IsFee(fund.Kind) {
				return nil, nil
			}
		}
	}
	ledgerBalance := funding - netPaid
	if !hasOpening || funding <= 0 || balance-ledgerBalance <= 1 {
		return nil, nil
	}
	rows, err := q.Query(`SELECT u.status,coalesce(st.state,'auto'),coalesce(r.released_at,''),coalesce(st.updated_at,''),coalesce(m.group_id,'')
 FROM pro_card_usage u LEFT JOIN nexovcc_case_states st ON st.order_id=u.order_id
 LEFT JOIN pro_card_order_releases r ON r.order_id=u.order_id
 LEFT JOIN nexovcc_case_matches m ON m.order_id=u.order_id WHERE u.card_key=?`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waiting := map[string]bool{}
	for rows.Next() {
		var status, state, requested, markedAt, groupID string
		if err = rows.Scan(&status, &state, &requested, &markedAt, &groupID); err != nil {
			return nil, err
		}
		if status == "failed" {
			if state == "opened" || state == "waiting" {
				return nil, nil
			}
			continue
		}
		g, ok := groups[groupID]
		if status != "success" || !ok || g.Payment == "0.00" {
			return nil, nil
		}
		if g.FullyReturned {
			if state == "opened" || state == "waiting" {
				return nil, nil
			}
			continue
		}
		if state == "waiting" {
			requested = markedAt
		} else if state != "auto" && state != "" {
			return nil, nil
		}
		at, err := time.Parse(time.RFC3339Nano, requested)
		if err != nil || at.Before(g.At) || at.After(card.BalanceAt) || at.After(ledgerAt) || g.Settled {
			return nil, nil
		}
		waiting[groupID] = true
	}
	if err = rows.Err(); err != nil || len(waiting) == 0 {
		return nil, err
	}
	for id, g := range groups {
		if g.Payment != "0.00" && !g.FullyReturned && !waiting[id] {
			return nil, nil // Unmatched/manual payments are not a Pro refund request.
		}
	}
	return &nexovcc.BalanceWithdrawal{Cycle: nexovcc.WithdrawFundingCycle(card, f), LedgerBalance: nexovcc.SignedMoney(ledgerBalance), Balance: card.Balance, BalanceAt: card.BalanceAt}, nil
}
