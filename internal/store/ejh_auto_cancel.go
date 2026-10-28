package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

var ErrEJHAutoCancelIneligible = errors.New("未满足自动销卡条件")

func (s *Store) EJHAutoCancelCheck(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var card ejh.CardRecord
	if err = s.ejhGetTx(tx, "card", id, &card); err != nil {
		return err
	}
	return s.ejhAutoCancelCheckTx(tx, card)
}

// Also called under the SQLite admission write lock immediately before the
// persistent cancellation claim. Other workers cannot reserve/pay this card
// between final validation and claiming it.
func (s *Store) ejhAutoCancelCheckTx(tx *sql.Tx, card ejh.CardRecord, verifiedAfter ...time.Time) error {
	skip := func(reason string) error { return fmt.Errorf("%w：%s", ErrEJHAutoCancelIneligible, reason) }
	var enabled bool
	if err := tx.QueryRow(`SELECT COALESCE((SELECT json_extract(profile,'$.enabled') AND json_extract(profile,'$.auto_cancel_enabled') FROM ejh_settings WHERE id=1),0)`).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return skip("自动销卡未启用")
	}
	if !ejh.AutoCancelNormalCard(card) {
		return skip("仅处理正常、未冻结的 OAI 卡片")
	}
	var key string
	if err := tx.QueryRow(`SELECT card_key FROM ejh_card_links WHERE card_id=? AND order_no=?`, card.ID, card.Remote.OrderNo).Scan(&key); err != nil {
		return err
	}
	kind, err := proCardType(tx, key)
	if err != nil {
		return err
	}
	if kind != "ejh" || key != proCardKey(card.Remote.Number) {
		return skip("卡片绑定不一致")
	}
	opened, _, err := proCardCounts(tx, key, "")
	if err != nil {
		return err
	}
	limit, err := proCardLimit(tx, key)
	if err != nil {
		return err
	}
	if limit < 1 || opened < limit {
		return skip("尚未达到累计开通上限")
	}
	var blocked bool
	err = tx.QueryRow(`SELECT
 EXISTS(SELECT 1 FROM pro_card_reservations WHERE card_key=?) OR
 EXISTS(SELECT 1 FROM pro_card_usage WHERE card_key=? AND status NOT IN ('success','failed')) OR
 EXISTS(SELECT 1 FROM pro_dead_cards WHERE card_key=?) OR
 EXISTS(SELECT 1 FROM ejh_operation_claims WHERE scope=?) OR
 EXISTS(SELECT 1 FROM ejh_records WHERE kind='operation' AND id=?) OR
 EXISTS(SELECT 1 FROM pro_card_usage u LEFT JOIN ejh_cases c ON c.id=u.order_id WHERE u.card_key=? AND u.status='success' AND (c.id IS NULL OR COALESCE(json_extract(c.profile,'$.payment_status'),'')<>'success'))`,
		key, key, key, "card:"+card.ID, EJHID("auto-cancel", card.ID), key).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return skip("存在占用、死号、既有销卡申请或不完整的付款记录")
	}
	cases, err := s.ejhCasesQuery(tx, nil)
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT sealed FROM ejh_records WHERE kind='trade'`)
	if err != nil {
		return err
	}
	var trades []ejh.TradeRecord
	for rows.Next() {
		var sealed string
		if err = rows.Scan(&sealed); err != nil {
			break
		}
		var raw string
		if raw, err = s.decrypt(sealed); err != nil {
			break
		}
		var trade ejh.TradeRecord
		if err = json.Unmarshal([]byte(raw), &trade); err != nil {
			break
		}
		if trade.CardID == card.ID {
			trades = append(trades, trade)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(verifiedAfter) > 0 {
		at := verifiedAfter[0]
		if at.IsZero() || at.After(time.Now()) || time.Since(at) > 2*time.Minute || card.SyncedAt.Before(at) {
			return skip("卡片最新资料未核实")
		}
		for _, trade := range trades {
			if (ejh.ChargeUsable(trade.Trade) || trade.Trade.IsReturn()) && trade.SyncedAt.Before(at) {
				return skip("本轮未查到完整付款和回款流水")
			}
		}
	}
	if err = ejh.ValidateAutoCancelReturns(card, cases, trades); err != nil {
		return skip(err.Error())
	}
	return nil
}
