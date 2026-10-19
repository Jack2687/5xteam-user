package store

import (
	"chatgpt-space-merge/internal/ejh"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EJHLinkCharge records attribution only. It never sends a payment or changes return state.
// An empty tradeID removes an explicit attribution, unless returns still depend on it.
func (s *Store) EJHLinkCharge(orderID, tradeID, evidence string) error {
	evidence = strings.TrimSpace(evidence)
	if len(evidence) < 3 {
		return fmt.Errorf("请填写业务对应关系或更正依据，不能仅凭金额和时间认定")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	trades, err := ejhRecordsLocked[ejh.TradeRecord](s, "trade")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow("SELECT profile FROM ejh_cases WHERE id=?", orderID).Scan(&raw); err != nil {
		return fmt.Errorf("开通订单不存在")
	}
	var c ejh.Case
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return err
	}
	var returns int
	if err = tx.QueryRow("SELECT count(*) FROM ejh_return_links WHERE order_id=?", orderID).Scan(&returns); err != nil {
		return err
	}
	if returns > 0 && tradeID != c.OriginalTradeID {
		return fmt.Errorf("已有回款依赖原交易，请先解除回款关联再更正")
	}
	if tradeID != "" {
		if c.PaymentStatus != "success" {
			return fmt.Errorf("只能关联成功开通订单")
		}
		var target *ejh.TradeRecord
		for i := range trades {
			if trades[i].ID == tradeID {
				target = &trades[i]
			}
		}
		if target == nil || (target.Trade.Type != "1" && target.Trade.Type != "4") {
			return fmt.Errorf("请选择授权或清算流水")
		}
		if target.CardID != c.CardID {
			var linked string
			if err = tx.QueryRow("SELECT l.card_id FROM pro_card_usage u JOIN ejh_card_links l ON l.card_key=u.card_key WHERE u.order_id=?", orderID).Scan(&linked); err != nil || linked != target.CardID {
				return fmt.Errorf("只能关联同一张实体卡的流水")
			}
		}
		if err = chargeAvailable(tx, trades, tradeID, orderID); err != nil {
			return err
		}
	}
	before := c
	c.OriginalTradeID, c.ChargeEvidence = tradeID, evidence
	c.ChargeLinkedAt = nil
	if tradeID == "" {
		c.ChargeEvidence = ""
	}
	if tradeID != "" {
		now := time.Now()
		c.ChargeLinkedAt = &now
	}
	if _, err = tx.Exec("UPDATE ejh_cases SET profile=? WHERE id=?", mustJSON(c), orderID); err != nil {
		return err
	}
	if err = s.ejhPutTx(tx, "match_audit", EJHID("audit", orderID+time.Now().String()), map[string]any{"action": "activation_charge", "before": before, "after": c, "reason": evidence, "at": time.Now()}); err != nil {
		return err
	}
	return tx.Commit()
}

type chargeQuery interface{ QueryRow(string, ...any) *sql.Row }

func chargeAvailable(q chargeQuery, trades []ejh.TradeRecord, tradeID, orderID string) error {
	var original *ejh.TradeRecord
	for i := range trades {
		if trades[i].ID == tradeID {
			original = &trades[i]
		}
	}
	if original == nil {
		return fmt.Errorf("原交易不存在")
	}
	// One authorization and its clearing may have different IDs but share a supplier authorization code.
	for _, t := range trades {
		same := t.ID == tradeID || (original.Trade.AuthCode != "" && t.CardID == original.CardID && t.Trade.AuthCode == original.Trade.AuthCode && (t.Trade.Type == "1" || t.Trade.Type == "4"))
		if !same {
			continue
		}
		var n int
		if err := q.QueryRow("SELECT count(*) FROM ejh_cases WHERE id<>? AND json_extract(profile,'$.original_trade_id')=?", orderID, t.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("该交易或同授权码流水已归属其他开通订单，不能重复记账")
		}
	}
	return nil
}
