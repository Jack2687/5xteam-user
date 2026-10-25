package store

import (
	"chatgpt-space-merge/internal/cardplatform"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func (s *Store) initializeNexovcc() error {
	_, e := s.db.Exec(`CREATE TABLE IF NOT EXISTS nexovcc_settings(id INTEGER PRIMARY KEY CHECK(id=1),profile TEXT NOT NULL,secret TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_cards(id TEXT PRIMARY KEY,serial TEXT NOT NULL UNIQUE,card_key TEXT UNIQUE,profile TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_cache(kind TEXT PRIMARY KEY,profile TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_operations(id TEXT PRIMARY KEY,scope TEXT NOT NULL,profile TEXT NOT NULL,status TEXT NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS nexovcc_active_scope ON nexovcc_operations(scope) WHERE status NOT IN ('succeeded','failed');
 CREATE UNIQUE INDEX IF NOT EXISTS nexovcc_restock_open ON nexovcc_operations(json_extract(profile,'$.restock_task_id')) WHERE scope='open' AND coalesce(json_extract(profile,'$.restock_task_id'),'')<>'';
 CREATE TABLE IF NOT EXISTS nexovcc_case_states(order_id TEXT PRIMARY KEY,state TEXT NOT NULL,reason TEXT NOT NULL,updated_at TEXT NOT NULL,revision INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_case_removals(order_id TEXT PRIMARY KEY,revision INTEGER NOT NULL,removed_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_audit(id INTEGER PRIMARY KEY AUTOINCREMENT,profile TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_case_matches(order_id TEXT PRIMARY KEY,group_id TEXT NOT NULL UNIQUE,returned INTEGER NOT NULL DEFAULT 0,profile TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_trade_purposes(group_id TEXT PRIMARY KEY,non_pro INTEGER NOT NULL,revision INTEGER NOT NULL,updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_trade_reviews(group_id TEXT PRIMARY KEY,profile TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nexovcc_revenue(identity TEXT PRIMARY KEY,email TEXT NOT NULL,amount REAL NOT NULL,updated_at TEXT NOT NULL);`)
	return e
}
func (s *Store) NexovccSettings() (nexovcc.Settings, nexovcc.Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nexovccSettingsLocked()
}
func (s *Store) nexovccSettingsLocked() (nexovcc.Settings, nexovcc.Credentials, error) {
	cfg := nexovcc.Defaults()
	var cred nexovcc.Credentials
	var raw, sealed string
	e := s.db.QueryRow(`SELECT profile,secret FROM nexovcc_settings WHERE id=1`).Scan(&raw, &sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return cfg.WithSyncDefaults(), cred, nil
	}
	if e != nil {
		return cfg, cred, e
	}
	if e = json.Unmarshal([]byte(raw), &cfg); e != nil {
		return cfg, cred, e
	}
	cfg = cfg.WithSyncDefaults()
	for i := range cfg.Templates {
		cfg.Templates[i].ProductIDs = cfg.Templates[i].OrderedProductIDs()
	}
	plain, e := s.decryptOptional(sealed)
	if e != nil {
		return cfg, cred, e
	}
	if plain != "" {
		e = json.Unmarshal([]byte(plain), &cred)
	}
	nexovccPresence(&cfg, cred)
	return cfg, cred, e
}
func nexovccPresence(c *nexovcc.Settings, k nexovcc.Credentials) {
	c.LoginPresent = k.Login != ""
	c.PasswordPresent = k.Password != ""
	c.FundPasswordPresent = k.FundPassword != ""
	c.TOTPPresent = k.TOTPSecret != ""
}
func (s *Store) SaveNexovccSettings(cfg nexovcc.Settings, patch nexovcc.Credentials, clearTOTP bool) (nexovcc.Settings, error) {
	cfg, e := nexovcc.Normalize(cfg)
	if e != nil {
		return cfg, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, old, e := s.nexovccSettingsLocked()
	if e != nil {
		return cfg, e
	}
	if patch.Login != "" {
		patch.Login = strings.TrimSpace(patch.Login)
		if patch.Login != old.Login {
			var n int
			if e = s.db.QueryRow(`SELECT (SELECT count(*) FROM nexovcc_cards)+(SELECT count(*) FROM nexovcc_operations)`).Scan(&n); e != nil {
				return cfg, e
			}
			if n > 0 {
				return cfg, fmt.Errorf("已有环球卡片或操作记录，不能切换商户混用账本")
			}
		}
		old.Login = patch.Login
	}
	if patch.Password != "" {
		old.Password = patch.Password
	}
	if patch.FundPassword != "" {
		old.FundPassword = patch.FundPassword
	}
	if clearTOTP {
		old.TOTPSecret = ""
	}
	if patch.TOTPSecret != "" {
		if _, e = nexovcc.TOTP(patch.TOTPSecret, time.Now()); e != nil {
			return cfg, e
		}
		old.TOTPSecret = patch.TOTPSecret
	}
	if cfg.Enabled && (old.Login == "" || old.Password == "" || old.FundPassword == "") {
		return cfg, fmt.Errorf("请先填写环球账号、登录密码及资金密码")
	}
	sealed, e := s.encrypt(mustJSON(old))
	if e != nil {
		return cfg, e
	}
	nexovccPresence(&cfg, old)
	_, e = s.db.Exec(`INSERT INTO nexovcc_settings VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET profile=excluded.profile,secret=excluded.secret`, mustJSON(cfg), sealed)
	return cfg, e
}
func (s *Store) NexovccCache(kind string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO nexovcc_cache VALUES(?,?) ON CONFLICT(kind) DO UPDATE SET profile=excluded.profile`, kind, mustJSON(value))
	return e
}
func (s *Store) NexovccCached(kind string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	e := s.db.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind=?`, kind).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	return json.Unmarshal([]byte(raw), value)
}
func (s *Store) NexovccCards() ([]nexovcc.Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT profile FROM nexovcc_cards ORDER BY json_extract(profile,'$.created_at') DESC,id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []nexovcc.Card{}
	for rows.Next() {
		var raw string
		var c nexovcc.Card
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &c); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) NexovccCard(id string) (nexovcc.Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c nexovcc.Card
	var raw string
	e := s.db.QueryRow(`SELECT profile FROM nexovcc_cards WHERE id=?`, id).Scan(&raw)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &c)
	}
	return c, e
}

// Snapshot, identity and private credentials become visible in one transaction.
func (s *Store) SaveNexovccCard(c nexovcc.Card, private *nexovcc.PrivateCard) (nexovcc.Card, error) {
	if c.Serial == "" {
		return c, fmt.Errorf("环球卡片缺少序列号")
	}
	c.ID = nexovcc.CardID(c.Serial)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	if e = lockProCardAdmission(tx); e != nil {
		return c, e
	}
	var raw string
	var oldKey sql.NullString
	var old nexovcc.Card
	e = tx.QueryRow(`SELECT profile,card_key FROM nexovcc_cards WHERE id=?`, c.ID).Scan(&raw, &oldKey)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return c, e
	}
	if raw != "" {
		if e = json.Unmarshal([]byte(raw), &old); e != nil {
			return c, e
		}
		c.Platform, c.TemplateID, c.TemplateName = old.Platform, old.TemplateID, old.TemplateName
		c.Imported = old.Imported
		// List and balance responses omit the holder; keep the last detail snapshot.
		if c.Holder == nil {
			c.Holder = old.Holder
		}
		c.NormalPending, c.NormalPendingAt = old.NormalPending, old.NormalPendingAt
		c.Control, c.ControlAt = old.Control, old.ControlAt
		// A transition back to ACTIVE after observing a non-active snapshot
		// also recognizes an unfreeze performed on the supplier's website.
		if c.Status == "4" || (c.Status == "1" && old.Status != "1") {
			c.Control = ""
		}
		if c.Last4 == "" {
			c.Last4 = old.Last4
		}
		if c.ProductID == "" {
			c.ProductID = old.ProductID
		}
		if c.Error == "" && private == nil {
			c.Error = old.Error
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = old.CreatedAt
		}
		if c.Balance == "" {
			c.Balance, c.BalanceAt = old.Balance, old.BalanceAt
		}
		if c.Status == "4" && old.Status == "4" {
			c.ClosedAt = old.ClosedAt
		}
	}
	if c.Platform == "" || c.TemplateID == "" {
		if e = setNexovccCardOrigin(tx, &c); e != nil {
			return c, e
		}
	}
	if c.Status == "4" {
		c.NormalPending, c.NormalPendingAt = false, nil
		if c.ClosedAt.IsZero() {
			var closedAt string
			if e = tx.QueryRow(`SELECT coalesce(max(json_extract(profile,'$.updated_at')),'') FROM nexovcc_operations WHERE scope=? AND json_extract(profile,'$.action') IN ('cancel','withdraw_cancel') AND status='succeeded'`, "card:"+c.ID).Scan(&closedAt); e != nil {
				return c, e
			}
			if closedAt != "" {
				c.ClosedAt, _ = time.Parse(time.RFC3339Nano, closedAt)
			}
			if c.ClosedAt.IsZero() {
				c.ClosedAt = c.SyncedAt
				if c.ClosedAt.IsZero() {
					c.ClosedAt = time.Now()
				}
			}
		}
	}
	// An import time is not falsely presented as the supplier's opening time.
	key := oldKey.String
	if private != nil {
		secret := gptpay.CardSecret{Number: private.Number, CVV: private.CVV, Month: private.Month, Year: private.Year}
		if e = gptpay.ValidateCard(secret, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)); e != nil {
			return c, fmt.Errorf("环球卡密校验失败：%w", e)
		}
		key = proCardKey(secret.Number)
		if oldKey.Valid && oldKey.String != key {
			return c, fmt.Errorf("环球序列号对应卡号发生变化，须人工核实")
		}
		var foreign bool
		if e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM ejh_card_links WHERE card_key=?)`, key).Scan(&foreign); e != nil {
			return c, e
		}
		if foreign {
			return c, fmt.Errorf("卡号已归属 EJH，禁止混用卡台")
		}
		card := gptpay.Card{ID: c.ID, Name: "环球 " + secret.Number[len(secret.Number)-4:], Enabled: true, CardType: "nexovcc", Source: "nexovcc", CreatedAt: c.CreatedAt}
		var profile string
		e = tx.QueryRow(`SELECT profile FROM gptpay_cards WHERE id=?`, c.ID).Scan(&profile)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return c, e
		}
		if profile != "" {
			if e = json.Unmarshal([]byte(profile), &card); e != nil {
				return c, e
			}
		}
		card.Platform, card.TemplateID, card.TemplateName = c.Platform, c.TemplateID, c.TemplateName
		card.CardType = "nexovcc"
		card.Source = "nexovcc"
		card.Last4 = secret.Number[len(secret.Number)-4:]
		card.ExpMonth = secret.Month
		card.ExpYear = secret.Year
		card.IssuerOrderNo = c.Serial
		card.IssuerStatus = c.Status
		card.UpdatedAt = time.Now()
		if card.CreatedAt.IsZero() {
			card.CreatedAt = card.UpdatedAt
		}
		sealed, e := s.encrypt(mustJSON(secret))
		if e != nil {
			return c, e
		}
		if e = saveProCardTypeTx(tx, c.ID, key, "nexovcc"); e != nil {
			return c, e
		}
		if _, e = tx.Exec(`INSERT INTO gptpay_cards VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET profile=excluded.profile,encrypted_secret=excluded.encrypted_secret`, c.ID, mustJSON(card), sealed, card.Enabled, formatTime(card.CreatedAt)); e != nil {
			return c, e
		}
		c.Imported = true
		c.Last4 = card.Last4
		c.Error = ""
	}
	var dbKey any
	if key != "" {
		dbKey = key
	}
	_, e = tx.Exec(`INSERT INTO nexovcc_cards VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET card_key=excluded.card_key,profile=excluded.profile`, c.ID, c.Serial, dbKey, mustJSON(c))
	if e != nil {
		return c, e
	}
	return c, tx.Commit()
}

// Save only holder fields so a manual copy cannot overwrite concurrent balance,
// category or lifecycle changes with its earlier card snapshot.
func (s *Store) SaveNexovccCardHolder(id string, holder *nexovcc.CardHolder) error {
	if holder == nil {
		return fmt.Errorf("环球未返回持卡人信息")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE nexovcc_cards SET profile=json_set(profile,'$.holder',json(?)) WHERE id=?`, mustJSON(holder), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) nexovccIssuer(q proCapacityQuery, key string) (*gptpay.Card, error) {
	var raw string
	var pending bool
	e := q.QueryRow(`SELECT c.profile,EXISTS(SELECT 1 FROM nexovcc_operations o WHERE o.scope='card:'||c.id AND o.status NOT IN ('succeeded','failed')) FROM nexovcc_cards c WHERE c.card_key=?`, key).Scan(&raw, &pending)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var c nexovcc.Card
	if e = json.Unmarshal([]byte(raw), &c); e != nil {
		return nil, e
	}
	out := &gptpay.Card{Platform: c.Platform, TemplateID: c.TemplateID, TemplateName: c.TemplateName, Source: "nexovcc", IssuerOrderNo: c.Serial, IssuerStatus: c.Status, IssuerFrozen: c.Control == "frozen", IssuerLocked: pending || c.Control == "frozen" || c.Error != "" || !c.Imported}
	var uncertain bool
	if e = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_card_usage WHERE card_key=? AND status IN ('submission_unknown','manual_review'))`, key).Scan(&uncertain); e != nil {
		return nil, e
	}
	out.IssuerLocked = out.IssuerLocked || uncertain
	if pending {
		var action, status string
		e = q.QueryRow(`SELECT json_extract(profile,'$.action'),status FROM nexovcc_operations WHERE scope=? AND status NOT IN ('succeeded','failed')`, "card:"+c.ID).Scan(&action, &status)
		if e != nil {
			return nil, e
		}
		if action == "cancel" || action == "withdraw_cancel" {
			out.IssuerCancelStatus = status
		}
	}
	return out, nil
}

const nexovccOccupiedSQL = `(CASE
 WHEN u.status NOT IN ('success','failed') THEN 1
 WHEN EXISTS(SELECT 1 FROM nexovcc_case_states s WHERE s.order_id=u.order_id AND s.state IN ('opened','waiting')) THEN 1
 WHEN EXISTS(SELECT 1 FROM nexovcc_case_states s WHERE s.order_id=u.order_id AND s.state IN ('returned','unopened')) THEN 0
 ELSE u.status='success' AND NOT EXISTS(SELECT 1 FROM nexovcc_case_matches m WHERE m.order_id=u.order_id AND m.returned=1)
 END)`

func nexovccOccupancy(q proCapacityQuery, key, except string) (int, string, error) {
	var n int
	var email string
	e := q.QueryRow(`SELECT count(*),coalesce(min(email),'') FROM (SELECT lower(email) email FROM pro_card_reservations WHERE card_key=? UNION SELECT lower(u.email) FROM pro_card_usage u WHERE u.card_key=? AND `+nexovccOccupiedSQL+`) WHERE email<>? COLLATE NOCASE`, key, key, except).Scan(&n, &email)
	return n, email, e
}
func nexovccSingleActive(q proCapacityQuery) (bool, error) {
	var enabled bool
	e := q.QueryRow(`SELECT COALESCE((SELECT json_extract(profile,'$.single_active_account') FROM nexovcc_settings WHERE id=1),1)`).Scan(&enabled)
	return enabled, e
}
func validateNexovccOccupancy(q proCapacityQuery, key, email string) error {
	enabled, e := nexovccSingleActive(q)
	if e != nil || !enabled {
		return e
	}
	n, owner, e := nexovccOccupancy(q, key, email)
	if e != nil {
		return e
	}
	if n > 0 {
		return fmt.Errorf("环球银行卡正在被 %s 占用，回款确认后才可再次分配", owner)
	}
	return nil
}
func nexovccOccupants(q ejhCaseQuery, key string) ([]gptpay.CardOccupant, error) {
	rows, e := q.Query(`SELECT a.email,a.order_id,a.status,m.email IS NULL FROM (SELECT lower(email) email,'' order_id,'reserved' status FROM pro_card_reservations WHERE card_key=? UNION ALL SELECT lower(u.email),u.order_id,u.status FROM pro_card_usage u WHERE u.card_key=? AND `+nexovccOccupiedSQL+`) a LEFT JOIN mail_accounts m ON m.email=a.email ORDER BY a.email,a.order_id`, key, key)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []gptpay.CardOccupant{}
	for rows.Next() {
		var v gptpay.CardOccupant
		if e = rows.Scan(&v.Email, &v.OrderID, &v.Status, &v.Deleted); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) NexovccOperations() ([]nexovcc.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT profile FROM nexovcc_operations ORDER BY json_extract(profile,'$.created_at') DESC,id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []nexovcc.Operation{}
	for rows.Next() {
		var raw string
		var v nexovcc.Operation
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) NexovccOperation(id string) (nexovcc.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	var v nexovcc.Operation
	e := s.db.QueryRow(`SELECT profile FROM nexovcc_operations WHERE id=?`, id).Scan(&raw)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &v)
	}
	return v, e
}

// NexovccOpenAdmission is supplied by the server after validating the manual
// choice. A claimed restock task always retains its own issuer and ownership.
type NexovccOpenAdmission struct {
	RestockID      string
	ManualProvider string
}

func (s *Store) BeginNexovccOperation(op nexovcc.Operation, admission ...NexovccOpenAdmission) error {
	if op.RestockTaskID != "" && (op.Action != "open" || !op.Automatic || len(admission) == 0 || admission[0].RestockID != op.RestockTaskID) {
		return fmt.Errorf("环球开卡与原补货任务不匹配")
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
	// Never accept caller-supplied refund/balance evidence.
	op.BalanceWithdrawal = nil
	scope := "open"
	if op.Action != "open" {
		scope = "card:" + op.CardID
		var raw string
		var key sql.NullString
		if e = tx.QueryRow(`SELECT profile,card_key FROM nexovcc_cards WHERE id=?`, op.CardID).Scan(&raw, &key); e != nil {
			return e
		}
		var c nexovcc.Card
		if e = json.Unmarshal([]byte(raw), &c); e != nil {
			return e
		}
		if c.Status == "4" {
			return fmt.Errorf("环球卡片已销卡，不能重复操作")
		}
		if (op.AutoWithdraw || op.CapacityRecycle) && !cardplatform.IsOAI(c.Platform) {
			return fmt.Errorf("CC 卡不参与自动提现或自动销卡")
		}
		if op.AutoWithdraw && c.NormalPending {
			return fmt.Errorf("正常待定卡不参与自动提现")
		}
		n, _, e := nexovccOccupancy(tx, key.String, "")
		if e != nil {
			return e
		}
		var pending int
		if e = tx.QueryRow(`SELECT (SELECT count(*) FROM pro_card_reservations WHERE card_key=?)+(SELECT count(*) FROM pro_card_usage WHERE card_key=? AND status NOT IN ('success','failed'))`, key.String, key.String).Scan(&pending); e != nil {
			return e
		}
		if pending > 0 {
			return fmt.Errorf("卡片仍有开通预留或未决付款，请先核实")
		}
		if op.AutoWithdraw {
			if op.Action != "withdraw_all" || op.CapacityRecycle {
				return fmt.Errorf("自动提现例外仅允许提现留 1U，不能用于销卡")
			}
			// Re-read the effective per-card limit under the admission lock: an
			// operator may have raised it after the background candidate scan.
			limit, err := proCardLimit(tx, key.String)
			if err != nil {
				return err
			}
			opened, _, err := proCardCounts(tx, key.String, "")
			if err != nil {
				return err
			}
			if opened < limit {
				return fmt.Errorf("自动提现须达到该卡开通上限（已开通 %d/%d）", opened, limit)
			}
			finance, err := nexovccWithdrawalFinance(tx)
			if err != nil {
				return err
			}
			if used, err := nexovccBalanceWithdrawalUsed(tx, c, finance); err != nil {
				return err
			} else if used {
				return fmt.Errorf("本轮恢复余额已有提现记录，退款流水补齐不会重复提现")
			}
		}
		if op.CapacityRecycle || op.AutoWithdraw {
			var unpaid int
			if e = tx.QueryRow(`SELECT count(*) FROM pro_card_usage u WHERE u.card_key=? AND u.status='success' AND NOT EXISTS(SELECT 1 FROM nexovcc_case_matches m WHERE m.order_id=u.order_id AND m.returned=1)`, key.String).Scan(&unpaid); e != nil {
				return e
			}
			if op.AutoWithdraw && unpaid > 0 {
				op.BalanceWithdrawal, e = nexovccBalanceWithdrawal(tx, c, key.String)
				if e != nil {
					return e
				}
				if op.BalanceWithdrawal != nil {
					balance, be := nexovcc.Cents(c.Balance)
					amount, ae := nexovcc.Cents(op.Amount)
					if be != nil || ae != nil || op.BeforeBalance != c.Balance || amount != balance-100 {
						return fmt.Errorf("可提现余额已变化，请重新核对")
					}
				}
			}
			if unpaid > 0 && op.BalanceWithdrawal == nil {
				return fmt.Errorf("自动提现或回收须等待全部付款实际回款，人工状态不能替代退款流水")
			}
		}
		if (op.Action == "withdraw" || op.Action == "withdraw_all" || op.Action == "cancel" || op.Action == "withdraw_cancel") && n > 0 && op.BalanceWithdrawal == nil {
			return fmt.Errorf("仍有未确认回款的账号，暂不能提现或销卡")
		}
	}
	if op.Action == "open" {
		var limit, count int
		if e = tx.QueryRow(`SELECT COALESCE((SELECT json_extract(profile,'$.max_online_cards') FROM nexovcc_settings WHERE id=1),0)`).Scan(&limit); e != nil {
			return e
		}
		if limit > 0 {
			if e = tx.QueryRow(`SELECT count(*) FROM nexovcc_cards WHERE coalesce(json_extract(profile,'$.status'),'')<>'4'`).Scan(&count); e != nil {
				return e
			}
			if count >= limit {
				return fmt.Errorf("环球在线卡数已达上限，须先完成旧卡销卡")
			}
		}
	}
	if e = validateNexovccAutomaticOpen(tx, op, admission...); e != nil {
		return e
	}

	_, e = tx.Exec(`INSERT INTO nexovcc_operations VALUES(?,?,?,?)`, op.ID, scope, mustJSON(op), op.Status)
	if e != nil {
		return fmt.Errorf("存在相同请求或待核实操作，请先查看环球操作记录")
	}
	return tx.Commit()
}

func validateNexovccAutomaticOpen(tx *sql.Tx, op nexovcc.Operation, admission ...NexovccOpenAdmission) error {
	if !op.Automatic {
		return nil
	}
	var e error
	var raw string
	if e = tx.QueryRow(`SELECT profile FROM pro_settings WHERE id=1`).Scan(&raw); e != nil {
		return e
	}
	cfg := model.DefaultProSettings()
	if e = json.Unmarshal([]byte(raw), &cfg); e != nil {
		return e
	}
	provider := cfg.AutoCardProvider()
	var choice NexovccOpenAdmission
	if len(admission) > 0 {
		choice = admission[0]
	}
	if choice.RestockID != "" {
		task, err := restockDispatchTask(tx, choice.RestockID)
		if err != nil {
			return err
		}
		provider = task.CardProvider
	} else if choice.ManualProvider != "" {
		if choice.ManualProvider != "ejh" && choice.ManualProvider != "nexovcc" {
			return fmt.Errorf("请选择 EJH 或环球卡台")
		}
		provider = choice.ManualProvider
	}
	if !cfg.NexovccAutoOpen || provider != "nexovcc" {
		return fmt.Errorf("环球自动补卡已关闭或切换")
	}
	var count int
	var amount int64
	day := op.CreatedAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	if e = tx.QueryRow(`SELECT count(*),coalesce(sum(CAST(round(CAST(json_extract(profile,'$.amount') AS REAL)*100) AS INTEGER)),0) FROM nexovcc_operations WHERE json_extract(profile,'$.automatic')=1 AND status<>'failed' AND date(json_extract(profile,'$.created_at'),'+8 hours')=?`, day).Scan(&count, &amount); e != nil {
		return e
	}
	cap, ce := nexovcc.Cents(cfg.NexovccDailyAmountLimit)
	add, ae := nexovcc.Cents(op.Amount)
	if ce != nil || ae != nil || count >= cfg.NexovccDailyOpenLimit || amount+add > cap {
		return fmt.Errorf("环球自动开卡已达每日数量或金额预算")
	}
	return nil
}

// Check the opening budget before closing an old card to make room. The final
// insertion repeats these checks under the admission transaction.
func (s *Store) CheckNexovccOpen(op nexovcc.Operation, admission ...NexovccOpenAdmission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var pending int
	if e = tx.QueryRow(`SELECT count(*) FROM nexovcc_operations WHERE scope='open' AND status NOT IN ('succeeded','failed')`).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return fmt.Errorf("存在未完成环球开卡，请先核实原操作")
	}
	return validateNexovccAutomaticOpen(tx, op, admission...)
}

func (s *Store) SaveNexovccOperation(op nexovcc.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op.UpdatedAt = time.Now()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.Exec(`UPDATE nexovcc_operations SET profile=?,status=? WHERE id=?`, mustJSON(op), op.Status, op.ID)
	if e != nil {
		return e
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return fmt.Errorf("环球操作记录不存在，更新未保存")
	}
	// The supplier's explicit success acknowledgement confirms freeze/unfreeze.
	// Persist it with the operation so a later card refresh cannot lose it.
	control, at := "", op.FrozenAt
	if !at.IsZero() {
		control = "frozen"
	}
	if op.Status == "succeeded" && (op.Action == "freeze" || op.Action == "unfreeze") {
		control, at = "frozen", op.StepAt
		if at.IsZero() {
			at = op.UpdatedAt
		}
		if op.Action == "unfreeze" {
			control = "active"
		}
	}
	if control != "" {
		_, e = tx.Exec(`UPDATE nexovcc_cards SET profile=json_set(profile,'$.control',?,'$.control_at',?) WHERE id=? AND coalesce(json_extract(profile,'$.control_at'),'')<?`, control, formatTime(at), op.CardID, formatTime(at))
		if e != nil {
			return e
		}
	}
	if _, e = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(op)); e != nil {
		return e
	}
	return tx.Commit()
}

type NexovccCase struct {
	MerchantFeeUSD string             `json:"merchant_fee_usd"`
	SharedFees     *NexovccSharedFees `json:"shared_fees,omitempty"`
	CardName       string             `json:"card_name"`
	Provider       string             `json:"provider"`
	OpenedAt       *time.Time         `json:"opened_at,omitempty"`
	Deleted        bool               `json:"deleted"`
	ManualState    string             `json:"manual_state,omitempty"`
	RemovedAt      string             `json:"removed_at,omitempty"`
	Match          *NexovccMatch      `json:"match,omitempty"`
	Revenue        *float64           `json:"revenue_r"`
	RevenueNote    string             `json:"revenue_note,omitempty"`
	ID             string             `json:"id"`
	Email          string             `json:"email"`
	CardID         string             `json:"card_id"`
	Last4          string             `json:"last4"`
	Plan           string             `json:"plan"`
	PaymentStatus  string             `json:"payment_status"`
	State          string             `json:"state"`
	Reason         string             `json:"reason"`
	RequestedAt    string             `json:"requested_at"`
	UpdatedAt      string             `json:"updated_at"`
	Revision       int                `json:"revision"`
	CreatedAt      time.Time          `json:"created_at"`
}

func (s *Store) NexovccCases() ([]NexovccCase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nexovccCasesQuery(s.db)
}

func (s *Store) nexovccCasesQuery(q nexovccCaseQuery) ([]NexovccCase, error) {
	merchantFees, err := proMerchantFeesByOrder(q)
	if err != nil {
		return nil, err
	}
	rows, e := q.Query(`SELECT o.id,o.email,c.id,json_extract(o.profile,'$.card_last4'),json_extract(o.profile,'$.plan_code'),u.status,coalesce(nullif(st.state,'auto'),CASE WHEN u.status<>'success' THEN 'unopened' WHEN r.order_id IS NOT NULL THEN 'waiting' ELSE 'opened' END),coalesce(st.reason,''),CASE WHEN st.state='waiting' THEN st.updated_at ELSE coalesce(r.released_at,'') END,coalesce(st.updated_at,''),coalesce(st.revision,0),o.created_at,coalesce(json_extract(o.profile,'$.card_name'),''),coalesce(json_extract(o.profile,'$.provider'),''),coalesce(json_extract(o.profile,'$.succeeded_at'),''),NOT EXISTS(SELECT 1 FROM mail_accounts ma WHERE ma.email=o.email AND json_extract(ma.profile,'$.management_scope')='pro'),coalesce(nullif(st.state,'auto'),''),coalesce(rm.removed_at,'') FROM pro_card_usage u JOIN gptpay_orders o ON o.id=u.order_id JOIN nexovcc_cards c ON c.card_key=u.card_key LEFT JOIN nexovcc_case_states st ON st.order_id=o.id LEFT JOIN pro_card_order_releases r ON r.order_id=o.id LEFT JOIN nexovcc_case_removals rm ON rm.order_id=o.id AND rm.revision=coalesce(st.revision,0) ORDER BY o.created_at DESC,o.id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []NexovccCase{}
	for rows.Next() {
		var v NexovccCase
		var at, openedAt string
		if e = rows.Scan(&v.ID, &v.Email, &v.CardID, &v.Last4, &v.Plan, &v.PaymentStatus, &v.State, &v.Reason, &v.RequestedAt, &v.UpdatedAt, &v.Revision, &at, &v.CardName, &v.Provider, &openedAt, &v.Deleted, &v.ManualState, &v.RemovedAt); e != nil {
			return nil, e
		}
		v.MerchantFeeUSD = merchantFees[v.ID]
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, at)
		if opened, err := time.Parse(time.RFC3339Nano, openedAt); err == nil && !opened.IsZero() {
			v.OpenedAt = &opened
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	if e = s.enrichNexovccCasesQuery(q, out); e != nil {
		return nil, e
	}
	for i := range out {
		if !out[i].canRemoveUnopened() {
			out[i].RemovedAt = ""
		}
	}
	return out, nil
}
func (s *Store) SetNexovccCase(id, state, reason string, revision int) error {
	if state != "auto" && state != "unopened" && state != "opened" && state != "waiting" && state != "returned" {
		return fmt.Errorf("回款状态无效")
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		return fmt.Errorf("请填写 3～500 字的核实依据")
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
	var status, key string
	var current int
	if e = tx.QueryRow(`SELECT u.status,u.card_key,coalesce(st.revision,0) FROM pro_card_usage u JOIN nexovcc_cards c ON c.card_key=u.card_key LEFT JOIN nexovcc_case_states st ON st.order_id=u.order_id WHERE u.order_id=?`, id).Scan(&status, &key, &current); e != nil {
		return e
	}
	if current != revision {
		return fmt.Errorf("记录已更新，请刷新后再修改")
	}
	if status != "success" && status != "failed" {
		return fmt.Errorf("付款结果未知，不能用回款状态解除占用")
	}
	if state == "opened" || state == "waiting" || state == "auto" {
		var n int
		if e = tx.QueryRow(`SELECT count(*) FROM nexovcc_operations o JOIN nexovcc_cards c ON o.scope='card:'||c.id WHERE c.card_key=? AND o.status NOT IN ('succeeded','failed')`, key).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fmt.Errorf("卡片有处理中操作，请完成核实后再修改状态")
		}
	}
	_, e = tx.Exec(`INSERT INTO nexovcc_case_states VALUES(?,?,?,?,?) ON CONFLICT(order_id) DO UPDATE SET state=excluded.state,reason=excluded.reason,updated_at=excluded.updated_at,revision=excluded.revision`, id, state, reason, formatTime(time.Now()), current+1)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{"order_id": id, "state": state, "reason": reason, "revision": current + 1, "at": time.Now()})); e != nil {
		return e
	}
	return tx.Commit()
}
