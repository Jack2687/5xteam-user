package store

import (
	"errors"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

var ErrEJHClosedCategory = errors.New("已销卡或销卡中的卡片不能移入正常或正常待定")

func (s *Store) SetEJHCardNormalPending(id string, pending bool) (ejh.CardRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var card ejh.CardRecord
	tx, err := s.db.Begin()
	if err != nil {
		return card, err
	}
	defer tx.Rollback()
	if err = s.ejhGetTx(tx, "card", id, &card); err != nil {
		return card, err
	}
	if card.Remote.Status == "2" || card.Remote.Status == "8" {
		return card, ErrEJHClosedCategory
	}
	if card.NormalPending == pending {
		return card, nil
	}
	now := time.Now()
	card.NormalPending, card.NormalPendingAt = pending, &now
	if err = s.ejhPutTx(tx, "card", id, card); err != nil {
		return card, err
	}
	if err = s.ejhPutTx(tx, "category_audit", id+"-"+now.Format(time.RFC3339Nano), map[string]any{"card_id": id, "normal_pending": pending, "at": now}); err != nil {
		return card, err
	}
	return card, tx.Commit()
}
