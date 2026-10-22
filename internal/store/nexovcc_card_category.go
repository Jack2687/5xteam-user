package store

import (
	"encoding/json"
	"errors"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

var ErrNexovccClosedCategory = errors.New("已销卡的卡片不能移入正常或正常待定")

// Classification is local metadata, separate from supplier status, availability,
// balances and automatic workflows. Read-modify-write under the store lock so
// a concurrent supplier refresh cannot overwrite it or lose newer card data.
func (s *Store) SetNexovccCardNormalPending(id string, pending bool) (nexovcc.Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var card nexovcc.Card
	tx, err := s.db.Begin()
	if err != nil {
		return card, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM nexovcc_cards WHERE id=?`, id).Scan(&raw); err != nil {
		return card, err
	}
	if err = json.Unmarshal([]byte(raw), &card); err != nil {
		return card, err
	}
	if card.Status == "4" {
		return card, ErrNexovccClosedCategory
	}
	if card.NormalPending == pending {
		return card, nil
	}
	previous := card.NormalPending
	now := time.Now()
	card.NormalPending, card.NormalPendingAt = pending, &now
	if _, err = tx.Exec(`UPDATE nexovcc_cards SET profile=? WHERE id=?`, mustJSON(card), id); err != nil {
		return card, err
	}
	if _, err = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{"action": "card_category", "card_id": id, "before_normal_pending": previous, "normal_pending": pending, "at": now})); err != nil {
		return card, err
	}
	return card, tx.Commit()
}
