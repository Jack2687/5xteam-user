package store

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"chatgpt-space-merge/internal/ejh"
)

type EJHCaseStateInput struct {
	State            string `json:"state"`
	Reason           string `json:"reason"`
	ExpectedState    string `json:"expected_state"`
	ExpectedRevision *int64 `json:"expected_revision"`
	Confirm          bool   `json:"confirm"`
}

// Manual state is an audited business override, never a fabricated transaction
// or a rewrite of the payment provider's order. An empty state restores auto.
func (s *Store) SetEJHCaseState(id string, in EJHCaseStateInput) (ejh.Case, error) {
	var out ejh.Case
	in.Reason = strings.TrimSpace(in.Reason)
	if !in.Confirm || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 {
		return out, fmt.Errorf("请确认调整并刷新账号的最新状态")
	}
	if !ejh.ValidCaseState(in.State) && in.State != "auto" {
		return out, fmt.Errorf("请选择未开通、已开通、已申请待回款或已回款")
	}
	if utf8.RuneCountInString(in.Reason) < 3 || utf8.RuneCountInString(in.Reason) > 600 {
		return out, fmt.Errorf("请填写 3～600 字的调整原因或核实依据")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return out, err
	}
	var email string
	if err = tx.QueryRow("SELECT email FROM ejh_cases WHERE id=?", id).Scan(&email); err != nil {
		return out, err
	}
	cases, err := s.ejhCasesQuery(tx, []string{email})
	if err != nil {
		return out, err
	}
	found := false
	for _, c := range cases {
		if c.ID == id {
			out, found = c, true
			break
		}
	}
	if !found || out.CardType != "ejh" {
		return out, fmt.Errorf("仅可调整 EJH 卡片的关联账号")
	}
	var revision int64
	if out.StateOverride != nil {
		revision = out.StateOverride.Revision
	}
	if revision != *in.ExpectedRevision || out.State != in.ExpectedState {
		return out, fmt.Errorf("账号回款状态已变化，请刷新后重新核对")
	}
	var status, key string
	if err = tx.QueryRow("SELECT status,card_key FROM pro_card_usage WHERE order_id=?", id).Scan(&status, &key); err != nil {
		return out, err
	}
	if in.State != "auto" && status != "success" && status != "failed" {
		return out, fmt.Errorf("付款仍在处理或结果未核实，请先查询原开通订单")
	}
	var reserved, operating bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pro_card_reservations WHERE email=? AND card_key=?)", email, key).Scan(&reserved); err != nil {
		return out, err
	}
	if reserved {
		return out, fmt.Errorf("该账号仍有开通预占，请先停止并核实任务")
	}
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM ejh_operation_claims WHERE scope IN (?,?))", "card:"+out.CardID, "card:"+out.EJHCardID).Scan(&operating); err != nil {
		return out, err
	}
	if operating {
		return out, fmt.Errorf("该卡有待处理资金操作，请核实完成后再调整")
	}
	state := in.State
	if state == "auto" {
		state = ""
	}
	change := ejh.CaseStateOverride{State: state, Reason: in.Reason, UpdatedAt: time.Now(), Revision: revision + 1}
	if _, err = tx.Exec(`INSERT INTO ejh_case_states(order_id,state,reason,updated_at,revision) VALUES(?,?,?,?,?)
 ON CONFLICT(order_id) DO UPDATE SET state=excluded.state,reason=excluded.reason,updated_at=excluded.updated_at,revision=excluded.revision`, id, state, in.Reason, formatTime(change.UpdatedAt), change.Revision); err != nil {
		return out, err
	}
	if err = s.ejhPutTx(tx, "match_audit", EJHID("audit", fmt.Sprintf("case-state:%s:%d", id, change.Revision)), map[string]any{"action": "manual_case_state", "order_id": id, "email": email, "card_id": out.CardID, "before_state": out.State, "before_override": out.StateOverride, "after": change, "at": change.UpdatedAt}); err != nil {
		return out, err
	}
	// An explicit state adjustment restores a previously removed return record.
	if _, err = tx.Exec(`UPDATE ejh_cases SET profile=json_remove(profile,'$.removed_at') WHERE id=?`, id); err != nil {
		return out, err
	}
	out.RemovedAt = nil
	out.StateOverride = &change
	out.State = out.AutomaticState
	if state != "" {
		out.State = state
	}
	// Only an explicit correction reopens the accounting rate. A temporarily
	// unconfirmed supplier refresh must not erase a previously locked fee.
	if out.State != "returned" {
		if _, err = tx.Exec(`DELETE FROM ejh_reversal_fee_rates WHERE order_id=?`, id); err != nil {
			return out, err
		}
	}
	if err = s.snapshotEJHReversalFeesTx(tx); err != nil {
		return out, err
	}
	rates, err := ejhReversalFeeRates(tx)
	if err != nil {
		return out, err
	}
	out.ReversalFee = rates[out.ID]
	for i := range out.Returns {
		out.Returns[i].ReversalFee = out.ReversalFee
	}
	return out, tx.Commit()
}

func ejhCaseStateOverrides(q ejhCaseQuery) (map[string]*ejh.CaseStateOverride, error) {
	rows, err := q.Query("SELECT order_id,state,reason,updated_at,revision FROM ejh_case_states")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ejh.CaseStateOverride{}
	for rows.Next() {
		var id, at string
		v := new(ejh.CaseStateOverride)
		if err = rows.Scan(&id, &v.State, &v.Reason, &at, &v.Revision); err != nil {
			return nil, err
		}
		if err = v.UpdatedAt.UnmarshalText([]byte(at)); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}
