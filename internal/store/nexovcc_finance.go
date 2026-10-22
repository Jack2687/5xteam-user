package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

type NexovccMatch struct {
	Group    nexovcc.TradeGroup `json:"group"`
	Method   string             `json:"method"`
	Evidence string             `json:"evidence,omitempty"`
}

func (s *Store) recordNexovccRevenueTx(tx *sql.Tx, p model.MailAccountProfile) error {
	if p.UserCostDownstreamIdentity == "" || p.CostConnectionIdentity == "" || p.CostCheckedAt == nil || math.IsNaN(p.UserCostDownstreamSnapshot) || math.IsInf(p.UserCostDownstreamSnapshot, 0) || p.UserCostDownstreamSnapshot < 0 {
		return nil
	}
	var eligible bool
	if e := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_card_usage u JOIN nexovcc_cards c ON c.card_key=u.card_key WHERE u.email=? AND u.status='success')`, p.Email).Scan(&eligible); e != nil {
		return e
	}
	if !eligible {
		return nil
	}
	_, e := tx.Exec(`INSERT INTO nexovcc_revenue VALUES(?,?,?,?) ON CONFLICT(identity) DO UPDATE SET amount=max(amount,excluded.amount),updated_at=excluded.updated_at`, p.CostConnectionIdentity+"|"+p.UserCostDownstreamIdentity, strings.ToLower(p.Email), p.UserCostDownstreamSnapshot, formatTime(*p.CostCheckedAt))
	return e
}

func (s *Store) CaptureNexovccRevenue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT profile FROM mail_accounts WHERE json_extract(profile,'$.management_scope')='pro'`)
	if e != nil {
		return e
	}
	profiles := []model.MailAccountProfile{}
	for rows.Next() {
		var raw string
		var p model.MailAccountProfile
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal([]byte(raw), &p); e != nil {
			rows.Close()
			return e
		}
		profiles = append(profiles, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, p := range profiles {
		if e = s.recordNexovccRevenueTx(tx, p); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Snapshot and matching are committed together, so readers never see a returned
// account while its financial evidence is still from an older partial sync.
func (s *Store) SaveNexovccFinance(f nexovcc.Finance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = lockProCardAdmission(tx); e != nil {
		return e
	}
	if e = s.saveNexovccFinanceTx(tx, f); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) saveNexovccFinanceTx(tx *sql.Tx, f nexovcc.Finance) error {
	if err := s.matchNexovccFinanceTx(tx, f); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO nexovcc_cache VALUES('finance',?) ON CONFLICT(kind) DO UPDATE SET profile=excluded.profile`, mustJSON(f))
	return err
}

// Rebuild derived matches from cached issuer evidence at startup. Closed and
// normal-pending cards are intentionally not polled, but parser fixes still
// need to update their historical costs and persisted return/admission flags.
// Preserve the original cache, manual states, purposes and review history.
func (s *Store) reconcileNexovccFinance() error {
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
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	var f nexovcc.Finance
	if err = json.Unmarshal([]byte(raw), &f); err != nil {
		return err
	}
	if err = s.matchNexovccFinanceTx(tx, f); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) matchNexovccFinanceTx(tx *sql.Tx, f nexovcc.Finance) error {
	rows, e := tx.Query(`SELECT o.profile,c.serial,u.status FROM pro_card_usage u JOIN gptpay_orders o ON o.id=u.order_id JOIN nexovcc_cards c ON c.card_key=u.card_key ORDER BY o.created_at`)
	if e != nil {
		return e
	}
	type candidate struct {
		order          gptpay.Order
		serial, status string
	}
	orders := []candidate{}
	for rows.Next() {
		var raw string
		var o candidate
		if e = rows.Scan(&raw, &o.serial, &o.status); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal([]byte(raw), &o.order); e != nil {
			rows.Close()
			return e
		}
		orders = append(orders, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	old := map[string]NexovccMatch{}
	rows, e = tx.Query(`SELECT order_id,profile FROM nexovcc_case_matches`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, raw string
		var m NexovccMatch
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal([]byte(raw), &m); e != nil {
			rows.Close()
			return e
		}
		old[id] = m
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	groups, e := nexovccTradeGroups(tx, f)
	if e != nil {
		return e
	}
	groupsByID := map[string]nexovcc.TradeGroup{}
	groupsBySerial := map[string][]nexovcc.TradeGroup{}
	for _, g := range groups {
		if g.NonPro {
			continue
		}
		groupsByID[g.ID] = g
		groupsBySerial[g.Serial] = append(groupsBySerial[g.Serial], g)
	}
	used := map[string]bool{}
	existing := map[string]bool{}
	byOrder := map[string][]nexovcc.TradeGroup{}
	byGroup := map[string][]string{}
	if _, e = tx.Exec(`DELETE FROM nexovcc_case_matches`); e != nil {
		return e
	}
	insert := func(id string, g nexovcc.TradeGroup, method, evidence string, status string) error {
		used[g.ID] = true
		existing[id] = true
		_, err := tx.Exec(`INSERT INTO nexovcc_case_matches VALUES(?,?,?,?)`, id, g.ID, g.Valid && g.FullyReturned && status == "success", mustJSON(NexovccMatch{g, method, evidence}))
		return err
	}
	for _, o := range orders {
		if m, ok := old[o.order.ID]; ok {
			if g, ok := groupsByID[m.Group.ID]; ok && g.Serial == o.serial {
				if e = insert(o.order.ID, g, m.Method, m.Evidence, o.status); e != nil {
					return e
				}
			}
		}
	}
	for _, o := range orders {
		if existing[o.order.ID] || o.status == "failed" {
			continue
		}
		for _, g := range groupsBySerial[o.serial] {
			if used[g.ID] || !g.Valid || g.RelatedGroupID != "" || g.Payment == "0.00" || g.Serial != o.serial || g.At.Before(o.order.CreatedAt.Add(-2*time.Minute)) || g.At.After(o.order.CreatedAt.Add(30*time.Minute)) {
				continue
			}
			r := o.order.Remote
			if r.PaymentCurrency != "" && (r.PaymentAmount != "" || r.PaymentAmountMinor != nil) {
				actualAmount := g.MerchantAmount
				if !strings.EqualFold(r.PaymentCurrency, g.MerchantCurrency) {
					if strings.EqualFold(r.PaymentCurrency, "USD") {
						actualAmount = g.Payment
					} else {
						continue
					}
				}
				expected, e := nexovcc.Cents(r.PaymentAmount)
				if r.PaymentAmount == "" && r.PaymentAmountMinor != nil {
					expected = *r.PaymentAmountMinor
					e = nil
				}
				actual, ae := nexovcc.Cents(actualAmount)
				if e != nil || ae != nil || expected != actual {
					continue
				}
			}
			byOrder[o.order.ID] = append(byOrder[o.order.ID], g)
			byGroup[g.ID] = append(byGroup[g.ID], o.order.ID)
		}
	}
	for _, o := range orders {
		gs := byOrder[o.order.ID]
		if o.status == "success" && len(gs) == 1 && len(byGroup[gs[0].ID]) == 1 {
			if e = insert(o.order.ID, gs[0], "auto", "同卡、时间窗口及可用付款金额唯一匹配", o.status); e != nil {
				return e
			}
		}
	}
	return nil
}

func (s *Store) MatchNexovccCase(id, groupID, reason string) error {
	if n := len([]rune(strings.TrimSpace(reason))); n < 5 || n > 500 {
		return fmt.Errorf("请填写 5～500 字的核实依据")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = lockProCardAdmission(tx); e != nil {
		return e
	}
	var serial, status, raw string
	if e = tx.QueryRow(`SELECT c.serial,u.status FROM pro_card_usage u JOIN nexovcc_cards c ON c.card_key=u.card_key WHERE u.order_id=?`, id).Scan(&serial, &status); e != nil {
		return e
	}
	if status != "success" {
		return fmt.Errorf("仅可为已确认付款成功的账号关联流水")
	}
	if e = tx.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw); e != nil {
		return e
	}
	var f nexovcc.Finance
	if e = json.Unmarshal([]byte(raw), &f); e != nil {
		return e
	}
	groups, e := nexovccTradeGroups(tx, f)
	if e != nil {
		return e
	}
	for _, g := range groups {
		if g.ID == groupID && g.NonPro {
			return fmt.Errorf("该付款已标记为非 Pro 付款，请先在交易明细中恢复匹配")
		}
		if g.ID == groupID && g.Serial == serial && g.Valid && g.Payment != "0.00" {
			var owner string
			e = tx.QueryRow(`SELECT order_id FROM nexovcc_case_matches WHERE group_id=?`, g.ID).Scan(&owner)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if owner != "" && owner != id {
				return fmt.Errorf("该流水已关联其他账号")
			}
			_, e = tx.Exec(`INSERT INTO nexovcc_case_matches VALUES(?,?,?,?) ON CONFLICT(order_id) DO UPDATE SET group_id=excluded.group_id,returned=excluded.returned,profile=excluded.profile`, id, g.ID, g.FullyReturned, mustJSON(NexovccMatch{g, "manual", reason}))
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{"order_id": id, "match_group": g.ID, "reason": reason, "at": time.Now()})); e != nil {
				return e
			}
			return tx.Commit()
		}
	}
	return fmt.Errorf("流水不存在、卡片不一致或交易信息不完整")
}

func (s *Store) enrichNexovccCasesQuery(q nexovccCaseQuery, cases []NexovccCase) error {
	// Recompute interpretations from the current cached evidence, including
	// parser corrections, without waiting for another supplier request.
	var financeRaw string
	var finance nexovcc.Finance
	var currentGroups map[string]nexovcc.TradeGroup
	if err := q.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&financeRaw); err == nil {
		if err = json.Unmarshal([]byte(financeRaw), &finance); err != nil {
			return err
		}
		groups, err := nexovccTradeGroups(q, finance)
		if err != nil {
			return err
		}
		currentGroups = map[string]nexovcc.TradeGroup{}
		for _, g := range groups {
			currentGroups[g.ID] = g
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	if err := allocateNexovccCardFees(q, cases, finance); err != nil {
		return err
	}
	revenues := map[string]*float64{}
	rows, e := q.Query(`SELECT email,sum(amount) FROM nexovcc_revenue GROUP BY email`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var email string
		var n float64
		if e = rows.Scan(&email, &n); e != nil {
			rows.Close()
			return e
		}
		revenues[strings.ToLower(email)] = &n
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Revenue is per downstream account, never repeated for multiple orders.
	assigned := map[string]bool{}
	for i := len(cases) - 1; i >= 0; i-- {
		c := &cases[i]
		email := strings.ToLower(c.Email)
		if c.PaymentStatus == "success" {
			if !assigned[email] {
				c.Revenue = revenues[email]
				assigned[email] = true
			} else {
				zero := float64(0)
				c.Revenue = &zero
				c.RevenueNote = "同账号收益已计入首次成功开通"
			}
		}
		var raw string
		e = q.QueryRow(`SELECT profile FROM nexovcc_case_matches WHERE order_id=?`, c.ID).Scan(&raw)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			var m NexovccMatch
			if e = json.Unmarshal([]byte(raw), &m); e != nil {
				return e
			}
			if currentGroups != nil {
				g, ok := currentGroups[m.Group.ID]
				if !ok {
					continue
				}
				m.Group = g
			}
			c.Match = &m
			var override string
			e = q.QueryRow(`SELECT state FROM nexovcc_case_states WHERE order_id=?`, c.ID).Scan(&override)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if (override == "" || override == "auto") && c.PaymentStatus == "success" && m.Group.FullyReturned && m.Group.Valid {
				c.State = "returned"
				c.UpdatedAt = formatTime(m.Group.ReturnedAt)
			}
		}
	}
	return nil
}
