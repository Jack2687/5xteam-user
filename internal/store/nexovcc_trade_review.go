package store

import (
	"chatgpt-space-merge/internal/nexovcc"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func nexovccTradeReviews(q ejhCaseQuery) ([]nexovcc.TradeReview, error) {
	rows, e := q.Query(`SELECT profile FROM nexovcc_trade_reviews`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []nexovcc.TradeReview{}
	for rows.Next() {
		var raw string
		var r nexovcc.TradeReview
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type NexovccReviewGroup struct {
	nexovcc.TradeGroup
	Fingerprint    string `json:"fingerprint"`
	ExpenseLimit   string `json:"expense_limit"`
	ExpenseProblem string `json:"expense_problem,omitempty"`
	Linked         bool   `json:"linked"`
}

func (s *Store) NexovccReviewData(f nexovcc.Finance) ([]NexovccReviewGroup, []nexovcc.TradeReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reviews, e := nexovccTradeReviews(s.db)
	if e != nil {
		return nil, nil, e
	}
	_, reviews = nexovcc.ReviewedGroups(f, reviews)
	groups := []NexovccReviewGroup{}
	for _, g := range nexovcc.Groups(f) {
		row := NexovccReviewGroup{TradeGroup: g, Fingerprint: nexovcc.ReviewFingerprint(f, g)}
		if e = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_case_matches WHERE group_id=?)`, g.ID).Scan(&row.Linked); e != nil {
			return nil, nil, e
		}
		n, err := nexovcc.OtherExpenseLimit(f, g)
		if err != nil {
			row.ExpenseProblem = err.Error()
		} else if row.Linked {
			row.ExpenseProblem = "已关联 Pro 账号，请先核实付款与回款"
		} else {
			row.ExpenseLimit = nexovcc.Money(n)
		}
		groups = append(groups, row)
	}
	return groups, reviews, nil
}

type NexovccReviewInput struct {
	Kind              string `json:"kind"`
	TargetID          string `json:"target_id"`
	Amount            string `json:"amount"`
	Reason            string `json:"reason"`
	Revision          int    `json:"revision"`
	Fingerprint       string `json:"fingerprint"`
	TargetFingerprint string `json:"target_fingerprint"`
	Voided            bool   `json:"voided"`
}

func (s *Store) SaveNexovccTradeReview(id string, in NexovccReviewInput) (nexovcc.TradeReview, error) {
	var result nexovcc.TradeReview
	in.Reason = strings.TrimSpace(in.Reason)
	if n := len([]rune(in.Reason)); n < 5 || n > 500 {
		return result, fmt.Errorf("请填写 5～500 字的处理原因")
	}
	if in.Revision < 0 {
		return result, fmt.Errorf("版本无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	if e = lockProCardAdmission(tx); e != nil {
		return result, e
	}
	var raw string
	var f nexovcc.Finance
	if e = tx.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw); e != nil {
		return result, fmt.Errorf("请先同步环球流水")
	}
	if e = json.Unmarshal([]byte(raw), &f); e != nil {
		return result, e
	}
	records, e := nexovccTradeReviews(tx)
	if e != nil {
		return result, e
	}
	var old nexovcc.TradeReview
	for _, r := range records {
		if r.GroupID == id {
			old = r
		}
	}
	if old.Revision != in.Revision {
		return result, fmt.Errorf("核实记录已变化，请刷新后重试")
	}
	result = nexovcc.TradeReview{GroupID: id, Kind: in.Kind, TargetID: in.TargetID, Reason: in.Reason, Revision: old.Revision + 1, UpdatedAt: time.Now(), Voided: in.Voided}
	if in.Voided {
		if old.Revision == 0 {
			return result, fmt.Errorf("没有可撤销的核实记录")
		}
		result.Kind, result.TargetID, result.Amount, result.Fingerprint = old.Kind, old.TargetID, old.Amount, old.Fingerprint
	} else {
		groups := nexovcc.Groups(f)
		var g, target nexovcc.TradeGroup
		for _, v := range groups {
			if v.ID == id {
				g = v
			}
			if v.ID == in.TargetID {
				target = v
			}
		}
		if g.ID == "" || nexovcc.ReviewFingerprint(f, g) != in.Fingerprint {
			return result, fmt.Errorf("交易流水已变化，请刷新后重新核实")
		}
		var linked bool
		if e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_case_matches WHERE group_id=?)`, g.ID).Scan(&linked); e != nil {
			return result, e
		}
		if linked {
			return result, fmt.Errorf("该组已关联 Pro 账号，不能直接改为其他费用或转移退款")
		}
		for _, r := range records {
			if r.GroupID != id && !r.Voided && (r.GroupID == in.TargetID || r.TargetID == id || in.TargetID != "" && r.TargetID == in.TargetID) {
				return result, fmt.Errorf("所选交易已有核实记录，请先撤销原记录")
			}
		}
		switch in.Kind {
		case "other_fee":
			if in.TargetID != "" {
				return result, fmt.Errorf("其他费用不应指定原付款")
			}
			limit, err := nexovcc.OtherExpenseLimit(f, g)
			if err != nil {
				return result, err
			}
			n, err := nexovcc.Cents(strings.TrimSpace(in.Amount))
			if err != nil || n <= 0 || n > limit {
				return result, fmt.Errorf("费用须大于 0，且不能超过此组净支出 %s USD", nexovcc.Money(limit))
			}
			result.Amount = nexovcc.Money(n)
			result.Fingerprint = nexovcc.ReviewFingerprint(f, g)
		case "refund_link":
			if target.ID == "" || nexovcc.ReviewFingerprint(f, target) != in.TargetFingerprint {
				return result, fmt.Errorf("原付款已变化，请重新选择")
			}
			if err := nexovcc.RefundLinkError(f, g, target); err != nil {
				return result, err
			}
			result.Fingerprint = nexovcc.ReviewFingerprint(f, g, target)
		default:
			return result, fmt.Errorf("请选择其他费用或关联原付款")
		}
	}
	if _, e = tx.Exec(`INSERT INTO nexovcc_trade_reviews VALUES(?,?) ON CONFLICT(group_id) DO UPDATE SET profile=excluded.profile`, id, mustJSON(result)); e != nil {
		return result, e
	}
	if _, e = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{"action": "trade_review", "before": old, "after": result, "at": time.Now()})); e != nil {
		return result, e
	}
	if e = s.saveNexovccFinanceTx(tx, f); e != nil {
		return result, e
	}
	return result, tx.Commit()
}
