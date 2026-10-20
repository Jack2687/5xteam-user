package nexovcc

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// A review changes accounting interpretation, never the supplier's raw ledger.
type TradeReview struct {
	GroupID     string    `json:"group_id"`
	Kind        string    `json:"kind"`
	TargetID    string    `json:"target_id,omitempty"`
	Amount      string    `json:"amount,omitempty"`
	Reason      string    `json:"reason"`
	Revision    int       `json:"revision"`
	Fingerprint string    `json:"fingerprint"`
	Voided      bool      `json:"voided"`
	UpdatedAt   time.Time `json:"updated_at"`
	Applied     bool      `json:"applied"`
	Problem     string    `json:"problem,omitempty"`
}

func ReviewFingerprint(f Finance, groups ...TradeGroup) string {
	ids := map[string]bool{}
	for _, g := range groups {
		for _, id := range append(append([]string{}, g.TradeIDs...), g.FeeTradeIDs...) {
			ids[id] = true
		}
	}
	rows := []Trade{}
	links := map[string]bool{}
	for _, r := range f.Trades {
		if ids[r.ID] {
			rows = append(rows, r)
			links[r.Serial+"|"+r.ID] = true
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	fees := []Fund{}
	for _, r := range f.Funds {
		if links[r.Serial+"|"+r.Link] {
			fees = append(fees, r)
		}
	}
	sort.Slice(fees, func(i, j int) bool { return fees[i].ID < fees[j].ID })
	raw, _ := json.Marshal(struct {
		Trades []Trade
		Funds  []Fund
	}{rows, fees})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Expense eligibility is deliberately separate from refund evidence. A manual
// cost never makes an unresolved card eligible for automatic withdrawal/closure.
func OtherExpenseLimit(f Finance, g TradeGroup) (int64, error) {
	if g.Valid {
		return 0, fmt.Errorf("该交易已可自动核算，无需重复记费用")
	}
	ids := map[string]bool{}
	for _, id := range g.TradeIDs {
		ids[id] = true
	}
	var pending, settled, unknown, returned int64
	for _, r := range f.Trades {
		if !ids[r.ID] {
			continue
		}
		n, e := Cents(r.Amount)
		if e != nil || r.Currency != "USD" {
			return 0, fmt.Errorf("须先核实 USD 金额")
		}
		switch r.Status {
		case "PENDING":
			pending += n
		case "COMPLETE":
			settled += n
		case "REFUNDED", "REVERSED":
			returned += n
		case "DECLINED", "EXPIRED":
		default:
			unknown += n
		}
	}
	n := max(pending, settled) + unknown - returned
	if n <= 0 {
		return 0, fmt.Errorf("此组没有可登记的净支出；退款请关联原付款，不能记成费用")
	}
	return n, nil
}

func RefundLinkError(f Finance, source, target TradeGroup) error {
	if source.ID == target.ID || source.Serial != target.Serial || !target.Valid || target.Authorization == "" {
		return fmt.Errorf("请选择同卡有效的原付款")
	}
	ids := map[string]bool{}
	for _, id := range source.TradeIDs {
		ids[id] = true
	}
	var refund int64
	for _, r := range f.Trades {
		if !ids[r.ID] {
			continue
		}
		n, e := Cents(r.Amount)
		if e != nil || r.Currency != "USD" || (r.Status != "REFUNDED" && r.Status != "REVERSED") || r.At.Before(target.At) {
			return fmt.Errorf("只可关联发生在原付款之后的独立 USD 退款")
		}
		refund += n
	}
	paid, e := Cents(target.Payment)
	back, be := Cents(target.Returned)
	if e != nil || be != nil || refund <= 0 || refund > paid-back {
		return fmt.Errorf("退款超过所选付款的未退金额")
	}
	return nil
}

func ReviewedGroups(f Finance, reviews []TradeReview) ([]TradeGroup, []TradeReview) {
	raw := Groups(f)
	byID := map[string]TradeGroup{}
	for _, g := range raw {
		byID[g.ID] = g
	}
	out := append([]TradeReview{}, reviews...)
	sort.Slice(out, func(i, j int) bool { return out[i].GroupID < out[j].GroupID })
	adjusted := f
	adjusted.Trades = append([]Trade{}, f.Trades...)
	claimed := map[string]bool{}
	for i := range out {
		r := &out[i]
		r.Applied = false
		r.Problem = ""
		if r.Voided {
			continue
		}
		g, ok := byID[r.GroupID]
		if !ok {
			r.Problem = "原交易已变化，请重新核实"
			continue
		}
		target := byID[r.TargetID]
		evidence := []TradeGroup{g}
		if r.Kind == "refund_link" {
			evidence = append(evidence, target)
		}
		if ReviewFingerprint(f, evidence...) != r.Fingerprint {
			r.Problem = "流水已变化，请重新核实"
			continue
		}
		if claimed[g.ID] || r.Kind == "refund_link" && claimed[target.ID] {
			r.Problem = "存在重叠核实记录"
			continue
		}
		switch r.Kind {
		case "other_fee":
			limit, e := OtherExpenseLimit(f, g)
			n, ne := Cents(r.Amount)
			if e != nil || ne != nil || n <= 0 || n > limit {
				r.Problem = "费用金额或原交易状态已变化"
				continue
			}
		case "refund_link":
			if e := RefundLinkError(f, g, target); e != nil {
				r.Problem = e.Error()
				continue
			}
			ids := map[string]bool{}
			for _, id := range g.TradeIDs {
				ids[id] = true
			}
			for j := range adjusted.Trades {
				if ids[adjusted.Trades[j].ID] {
					adjusted.Trades[j].Authorization = target.Authorization
				}
			}
			claimed[target.ID] = true
		default:
			r.Problem = "未知核实方式"
			continue
		}
		claimed[g.ID] = true
		r.Applied = true
	}
	groups := Groups(adjusted)
	for i := range groups {
		for j := range out {
			if out[j].Applied && out[j].Kind == "other_fee" && out[j].GroupID == groups[i].ID {
				groups[i].OtherExpense = &out[j]
			}
		}
	}
	return groups, out
}
