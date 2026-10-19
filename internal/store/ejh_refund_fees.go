package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

// Refund fees are explicitly recorded expenses, not inferred from a balance gap.
// A balance reconciliation retains its source instead of claiming issuer proof.
type EJHRefundFee struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Amount    string    `json:"amount"`
	Source    string    `json:"source"`
	Note      string    `json:"note"`
	Voided    bool      `json:"voided"`
	Revision  int       `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ejhRefundFeeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)
var ErrEJHRefundFeeConflict = errors.New("费用记录已更新，请刷新后重试")

func (s *Store) EJHRefundFees() ([]EJHRefundFee, error) {
	rows, err := EJHRecords[EJHRefundFee](s, "finance_refund_fee")
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At.Equal(rows[j].At) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].At.After(rows[j].At)
	})
	return rows, err
}

func (s *Store) SaveEJHRefundFee(in EJHRefundFee) (EJHRefundFee, error) {
	in.Amount, in.Note = strings.TrimSpace(in.Amount), strings.TrimSpace(in.Note)
	if !ejhRefundFeeID.MatchString(in.ID) || in.Revision < 0 {
		return in, fmt.Errorf("费用记录编号或版本无效")
	}
	if !ejhLedgerAmount.MatchString(in.Amount) || ejh.Positive(in.Amount) != nil {
		return in, fmt.Errorf("退款手续费须为正数 USD 金额，最多两位小数")
	}
	amount, _ := ejh.Money(in.Amount)
	in.Amount = amount.FloatString(2)
	if in.At.IsZero() || in.At.Year() < 2000 || in.At.After(time.Now()) {
		return in, fmt.Errorf("请选择有效的费用发生时间，不能晚于当前时间")
	}
	if in.Source != "manual" && in.Source != "balance_reconciliation" {
		return in, fmt.Errorf("请选择费用来源")
	}
	if in.Note == "" || len([]rune(in.Note)) > 500 {
		return in, fmt.Errorf("请填写费用说明，最多500字")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var old EJHRefundFee
	err = s.ejhGetTx(tx, "finance_refund_fee", in.ID, &old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return in, err
	}
	if err == nil && old.Amount == in.Amount && old.At.Equal(in.At) && old.Source == in.Source && old.Note == in.Note && old.Voided == in.Voided {
		return old, nil
	}
	if old.Revision != in.Revision || old.Revision == 0 && in.Voided {
		return in, ErrEJHRefundFeeConflict
	}
	in.Revision = old.Revision + 1
	in.UpdatedAt = time.Now()
	if err = s.ejhPutTx(tx, "finance_refund_fee", in.ID, in); err != nil {
		return in, err
	}
	audit := map[string]any{"before": old, "after": in, "at": in.UpdatedAt}
	if err = s.ejhPutTx(tx, "finance_refund_fee_audit", fmt.Sprintf("%s:%d", in.ID, in.Revision), audit); err != nil {
		return in, err
	}
	return in, tx.Commit()
}
