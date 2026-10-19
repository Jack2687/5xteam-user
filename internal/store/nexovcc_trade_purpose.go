package store

import (
	"encoding/json"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Purpose belongs to the issuer authorization, so later refund/settlement rows
// inherit it without altering the original financial records or refund proof.
func nexovccTradeGroups(q ejhCaseQuery, f nexovcc.Finance) ([]nexovcc.TradeGroup, error) {
	rows, err := q.Query(`SELECT group_id,non_pro,revision FROM nexovcc_trade_purposes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type purpose struct {
		nonPro   bool
		revision int
	}
	marks := map[string]purpose{}
	for rows.Next() {
		var id string
		var p purpose
		if err = rows.Scan(&id, &p.nonPro, &p.revision); err != nil {
			return nil, err
		}
		marks[id] = p
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	groups := nexovcc.Groups(f)
	for i := range groups {
		p := marks[groups[i].ID]
		groups[i].NonPro, groups[i].PurposeRevision = p.nonPro, p.revision
	}
	return groups, nil
}

func (s *Store) NexovccTradeGroups(f nexovcc.Finance) ([]nexovcc.TradeGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nexovccTradeGroups(s.db, f)
}

func (s *Store) SetNexovccTradePurpose(tradeID string, nonPro bool, revision int) (nexovcc.TradeGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result nexovcc.TradeGroup
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return result, err
	}
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw); err != nil {
		return result, fmt.Errorf("请先同步环球交易流水")
	}
	var f nexovcc.Finance
	if err = json.Unmarshal([]byte(raw), &f); err != nil {
		return result, err
	}
	groups, err := nexovccTradeGroups(tx, f)
	if err != nil {
		return result, err
	}
	for _, g := range groups {
		for _, id := range g.TradeIDs {
			if id == tradeID {
				if result.ID != "" && result.ID != g.ID {
					return result, fmt.Errorf("交易编号不唯一，请同步后核实")
				}
				result = g
			}
		}
	}
	if result.ID == "" {
		return result, fmt.Errorf("交易不存在，请刷新后重试")
	}
	if revision != result.PurposeRevision || revision < 0 {
		return result, fmt.Errorf("付款标记已变化，请刷新后重试")
	}
	if result.NonPro == nonPro {
		return result, nil
	}
	if nonPro {
		var linked bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_case_matches WHERE group_id=?)`, result.ID).Scan(&linked); err != nil {
			return result, err
		}
		if linked {
			return result, fmt.Errorf("该付款已关联 Pro 账号，不能直接标记为非 Pro 付款")
		}
		payment, e := nexovcc.Cents(result.Payment)
		if e != nil || payment <= 0 {
			return result, fmt.Errorf("缺少原付款记录，暂不能标记")
		}
	}
	if _, err = tx.Exec(`INSERT INTO nexovcc_trade_purposes VALUES(?,?,?,?) ON CONFLICT(group_id) DO UPDATE SET non_pro=excluded.non_pro,revision=excluded.revision,updated_at=excluded.updated_at`, result.ID, nonPro, revision+1, formatTime(time.Now())); err != nil {
		return result, err
	}
	if _, err = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{"action": "trade_purpose", "trade_id": tradeID, "group_id": result.ID, "before": result.NonPro, "non_pro": nonPro, "revision": revision + 1, "at": time.Now()})); err != nil {
		return result, err
	}
	// Re-evaluate ambiguity immediately; classifications and account matches
	// become visible together. Restoring can resume normal unique matching.
	if err = s.saveNexovccFinanceTx(tx, f); err != nil {
		return result, err
	}
	result.NonPro, result.PurposeRevision = nonPro, revision+1
	return result, tx.Commit()
}
