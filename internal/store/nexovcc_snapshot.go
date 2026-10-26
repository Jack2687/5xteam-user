package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Card balance, both ledgers and derived return/admission evidence are one
// financial snapshot. A failed read never calls this method.
func (s *Store) SaveNexovccCardSnapshot(card nexovcc.Card, fresh nexovcc.Finance) error {
	if card.Serial == "" || fresh.At.IsZero() {
		return fmt.Errorf("卡片财务快照不完整")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return err
	}
	// Use the current local metadata: classification or limits can change while
	// supplier reads are in flight. Only the queried balance is replaced here.
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM nexovcc_cards WHERE id=?`, card.ID).Scan(&raw); err != nil {
		return err
	}
	var current nexovcc.Card
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if current.Serial != card.Serial {
		return fmt.Errorf("卡片财务快照归属不符")
	}
	current.Balance, current.BalanceAt = card.Balance, card.BalanceAt
	if strings.HasPrefix(current.Error, "余额与流水同步失败：") || strings.HasPrefix(current.Error, "余额同步失败：") {
		current.Error = ""
	}
	if _, err = s.saveNexovccCardTx(tx, current, nil, true); err != nil {
		return err
	}
	var previous nexovcc.Finance
	err = tx.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
	}
	if previous.CardAt == nil {
		cards, e := nexovccCardsQuery(tx)
		if e != nil {
			return e
		}
		previous.CardAt = map[string]time.Time{}
		for _, c := range cards {
			previous.CardAt[c.Serial] = previous.At
		}
	}
	if err = s.saveNexovccFinanceTx(tx, nexovcc.MergeCardFinance(previous, fresh, card.Serial)); err != nil {
		return err
	}
	return tx.Commit()
}

// Record a failed attempt without overwriting any balance, timestamps or
// concurrently changed local card metadata.
func (s *Store) SetNexovccCardSyncError(id, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE nexovcc_cards SET profile=json_set(profile,'$.error',?) WHERE id=?`, message, id)
	return err
}

// Read the same committed generation for the dashboard. Separate reads could
// otherwise straddle an atomic card snapshot commit during a long scan.
func (s *Store) NexovccFinancialSnapshot() (cards []nexovcc.Card, finance nexovcc.Finance, cases []NexovccCase, groups []nexovcc.TradeGroup, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if cards, err = nexovccCardsQuery(tx); err != nil {
		return
	}
	var raw string
	err = tx.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw)
	if err == sql.ErrNoRows {
		err = nil
	} else if err == nil {
		err = json.Unmarshal([]byte(raw), &finance)
	}
	if err != nil {
		return
	}
	if cases, err = s.nexovccCasesQuery(tx); err != nil {
		return
	}
	groups, err = nexovccTradeGroups(tx, finance)
	return
}
