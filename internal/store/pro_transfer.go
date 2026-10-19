package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

const ProTransferFormat = "space-pro-accounts"
const ProTransferVersion = 1

type ProTransferFile struct {
	Format     string               `json:"format"`
	Version    int                  `json:"version"`
	ExportedAt time.Time            `json:"exported_at"`
	Accounts   []ProTransferAccount `json:"accounts"`
}
type ProTransferOrder struct {
	ReturnRequestedAt *time.Time             `json:"return_requested_at,omitempty"`
	CaseStateOverride *ejh.CaseStateOverride `json:"case_state_override,omitempty"`
	ReturnConfirmed   bool                   `json:"return_confirmed,omitempty"`
	CardType          string                 `json:"card_type,omitempty"`
	OccupancyReleased *bool                  `json:"occupancy_released,omitempty"`
	Order             gptpay.Order           `json:"order"`
	Snapshot          gptpay.Snapshot        `json:"snapshot"`
}
type ProTransferAccount struct {
	Profile      model.MailAccountProfile     `json:"profile"`
	Credentials  model.MailAccountCredentials `json:"credentials"`
	Orders       []ProTransferOrder           `json:"orders"`
	TargetTeamID string                       `json:"target_team_id,omitempty"`
	Downstream   model.ProDownstreamRef       `json:"downstream"`
	VM2Target    *model.DownstreamTarget      `json:"vm2api_target,omitempty"`
}
type ProTransferResult struct {
	Email   string `json:"email"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// One local snapshot; never refresh a token, query an upstream, or interrupt
// a live task just to export. Secrets are emitted only by the explicit download.
func (s *Store) ExportProAccounts(emails []string, downstream model.ProDownstreamRef) (ProTransferFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ProTransferFile{Format: ProTransferFormat, Version: ProTransferVersion, ExportedAt: time.Now().UTC(), Accounts: []ProTransferAccount{}}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	overrides, err := ejhCaseStateOverrides(tx)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for _, email := range emails {
		email = strings.ToLower(strings.TrimSpace(email))
		if seen[email] {
			continue
		}
		seen[email] = true
		var raw, sealed string
		if err = tx.QueryRow("SELECT profile,encrypted_credentials FROM mail_accounts WHERE email=?", email).Scan(&raw, &sealed); err != nil {
			return out, errors.New("导出账号不存在，请刷新列表")
		}
		entry := ProTransferAccount{Orders: []ProTransferOrder{}}
		if err = json.Unmarshal([]byte(raw), &entry.Profile); err != nil {
			return out, err
		}
		if entry.Profile.ManagementScope != "pro" {
			return out, errors.New("只能导出 Pro 管理中的账号")
		}
		plain, e := s.decrypt(sealed)
		if e != nil {
			return out, errors.New("读取账号凭证失败")
		}
		if err = json.Unmarshal([]byte(plain), &entry.Credentials); err != nil {
			return out, errors.New("账号凭证格式无效")
		}
		entry.TargetTeamID = entry.Profile.TargetTeamID
		if entry.TargetTeamID == "" && entry.Profile.TargetAdminID != "" {
			var admin model.AdminAccountProfile
			if e = tx.QueryRow("SELECT profile FROM admin_accounts WHERE id=?", entry.Profile.TargetAdminID).Scan(&raw); e == nil {
				if e = json.Unmarshal([]byte(raw), &admin); e != nil {
					return out, e
				}
				entry.TargetTeamID = admin.TeamAccountID
			}
			if entry.TargetTeamID == "" && entry.Profile.ProMigration != nil {
				entry.TargetTeamID = entry.Profile.ProMigration.TargetTeamID
			}
		}
		entry.Downstream = downstream
		if entry.Profile.ProRestockTargetID != "" {
			var encrypted string
			if err = tx.QueryRow("SELECT encrypted_payload FROM downstream_connections WHERE id=?", entry.Profile.ProRestockTargetID).Scan(&encrypted); err != nil {
				return out, err
			}
			plain, e := s.decrypt(encrypted)
			if e != nil {
				return out, e
			}
			var target model.DownstreamTarget
			if e = json.Unmarshal([]byte(plain), &target); e != nil {
				return out, e
			}
			entry.Downstream = model.ProDownstream(model.ProSettings{Provider: target.Provider, Sub2: target.Sub2})
		}
		if entry.Profile.ProMigration != nil {
			entry.Downstream = entry.Profile.ProMigration.Downstream
		}
		if entry.Profile.PushProvider != "" && entry.Profile.PushProvider != entry.Downstream.Provider {
			entry.Downstream = model.ProDownstreamRef{Provider: entry.Profile.PushProvider}
		}
		if entry.Profile.VM2 != nil {
			var encrypted string
			if err = tx.QueryRow("SELECT encrypted_payload FROM downstream_connections WHERE id=?", entry.Profile.VM2.ConnectionID).Scan(&encrypted); err != nil {
				return out, errors.New("vm2api 绑定快照缺失，无法完整导出")
			}
			plain, e := s.decrypt(encrypted)
			if e != nil {
				return out, e
			}
			var target model.DownstreamTarget
			if err = json.Unmarshal([]byte(plain), &target); err != nil {
				return out, err
			}
			entry.VM2Target = &target
			entry.Downstream = model.ProDownstream(model.ProSettings{Provider: "vm2api", VM2API: target.VM2API})
		}
		rows, e := tx.Query(`SELECT o.profile,o.encrypted_snapshot,COALESCE(k.card_type,'other'),EXISTS(SELECT 1 FROM pro_card_order_releases r WHERE r.order_id=o.id),EXISTS(SELECT 1 FROM ejh_received_returns r WHERE r.order_id=o.id) FROM gptpay_orders o LEFT JOIN pro_card_usage u ON u.order_id=o.id LEFT JOIN pro_card_types k ON k.card_key=u.card_key WHERE o.email=? ORDER BY o.created_at,o.id`, email)
		if e != nil {
			return out, e
		}
		foundOrder := entry.Profile.ProAuto.OrderID == ""
		for rows.Next() {
			var order ProTransferOrder
			var released bool
			if e = rows.Scan(&raw, &sealed, &order.CardType, &released, &order.ReturnConfirmed); e != nil {
				break
			}
			if order.CardType == "ejh" {
				released = order.ReturnConfirmed
			}
			order.OccupancyReleased = &released
			if e = json.Unmarshal([]byte(raw), &order.Order); e != nil {
				break
			}
			plain, e = s.decrypt(sealed)
			if e != nil {
				break
			}
			if e = json.Unmarshal([]byte(plain), &order.Snapshot); e != nil {
				break
			}
			if order.Order.ID == entry.Profile.ProAuto.OrderID {
				foundOrder = true
			}
			order.CaseStateOverride = overrides[order.Order.ID]
			entry.Orders = append(entry.Orders, order)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return out, errors.New("读取账号订单快照失败")
		}
		if !foundOrder {
			return out, errors.New("账号正在保存订单，请稍后重新导出")
		}
		if err = exportNexovccTransferStates(tx, entry.Orders); err != nil {
			return out, err
		}
		out.Accounts = append(out.Accounts, entry)
	}
	return out, tx.Commit()
}

func ValidateProTransfer(file ProTransferFile) error {
	if file.Format != ProTransferFormat || file.Version != ProTransferVersion {
		return errors.New("不支持的 Pro 迁移文件格式或版本")
	}
	if file.ExportedAt.IsZero() || len(file.Accounts) == 0 || len(file.Accounts) > 10000 {
		return errors.New("迁移文件需包含导出时间及 1～10000 个账号")
	}
	seen := map[string]bool{}
	for _, a := range file.Accounts {
		email := strings.ToLower(strings.TrimSpace(a.Profile.Email))
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email || seen[email] {
			return errors.New("迁移文件包含无效或重复邮箱")
		}
		seen[email] = true
		if a.Profile.ManagementScope != "pro" || !strings.EqualFold(strings.TrimSpace(a.Credentials.Email), email) {
			return errors.New("迁移账号归属或凭证邮箱不匹配")
		}
		if a.Credentials.ChatGPTSession != "" && !json.Valid([]byte(a.Credentials.ChatGPTSession)) {
			return errors.New("迁移文件中的 Session 格式无效")
		}
		if binding := a.Profile.VM2; binding != nil {
			hash := sha256.Sum256([]byte(binding.AssignmentID))
			if !strings.HasPrefix(binding.AssignmentID, "space-") || len(binding.AssignmentID) != 54 || binding.SlotID != fmt.Sprintf("space-%x", hash[:12]) || a.Profile.PushProvider != "vm2api" || a.VM2Target == nil || a.VM2Target.Provider != "vm2api" || a.VM2Target.Secret == "" || a.VM2Target.VM2API.URL == "" || binding.RuntimeType != a.VM2Target.VM2API.RuntimeType {
				return errors.New("迁移文件中的 vm2api 绑定或连接快照不完整")
			}
		}
		orderIDs := map[string]bool{}
		active := 0
		for _, o := range a.Orders {
			if v := o.CaseStateOverride; v != nil {
				if (o.CardType != "ejh" && o.CardType != "nexovcc") || (!ejh.ValidCaseState(v.State) && v.State != "") || strings.TrimSpace(v.Reason) == "" || len(v.Reason) > 2400 || v.UpdatedAt.IsZero() || v.Revision < 1 {
					return errors.New("迁移文件人工回款状态无效")
				}
			}
			if o.CardType != "" && o.CardType != "ejh" && o.CardType != "other" && o.CardType != "nexovcc" {
				return errors.New("迁移订单银行卡类型无效")
			}
			if o.Order.ID == "" || len(o.Order.ID) > 160 || orderIDs[o.Order.ID] || !strings.EqualFold(o.Order.Email, email) {
				return errors.New("迁移文件订单编号或邮箱不匹配")
			}
			orderIDs[o.Order.ID] = true
			if o.Order.Active() {
				active++
			}
			if o.Order.Status != "submitting" && o.Order.Status != "submission_unknown" && !gptpay.ValidStatus(o.Order.Status) {
				return errors.New("迁移文件订单状态无效")
			}
			if o.Order.CreatedAt.IsZero() {
				return errors.New("迁移文件订单缺少创建时间")
			}
			if o.Snapshot.Input.Session.User.Email != "" && !strings.EqualFold(o.Snapshot.Input.Session.User.Email, email) {
				return errors.New("订单快照账号不匹配")
			}
			if o.Snapshot.APIKey == "" || o.Snapshot.URL == "" {
				return errors.New("订单缺少查询凭据，不能完整迁移")
			}
			if gptpay.Provider(o.Order.Provider) != gptpay.Provider(o.Snapshot.Provider) {
				return errors.New("订单供应商与快照不一致")
			}
			if o.Order.PlanCode != o.Snapshot.Input.PlanCode || o.Order.CreditQuantity != o.Snapshot.Input.CreditQuantity ||
				(o.Snapshot.Input.Country != "" && (o.Order.Country != o.Snapshot.Input.Country || o.Snapshot.Country != o.Snapshot.Input.Country)) {
				return errors.New("订单套餐、国家或点数与快照不一致")
			}
			if _, err = gptpay.NormalizeSettings(gptpay.Settings{Provider: o.Snapshot.Provider, Country: o.Snapshot.Country, URL: o.Snapshot.URL, PlanCode: o.Order.PlanCode, CreditQuantity: o.Snapshot.Input.CreditQuantity}); err != nil {
				return errors.New("订单供应商地址或套餐无效")
			}
		}
		if active > 1 {
			return errors.New("同账号存在多个未完成订单")
		}
		if a.Profile.ProAuto.OrderID != "" && !orderIDs[a.Profile.ProAuto.OrderID] {
			return errors.New("迁移文件缺少全自动流程关联的订单")
		}
		if a.Profile.TargetTeamID != "" && a.TargetTeamID != "" && a.Profile.TargetTeamID != a.TargetTeamID {
			return errors.New("迁移文件中的空间关联冲突")
		}
	}
	return nil
}

func proTransferID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("pro-import-%d", time.Now().UnixNano())
	}
	return "pro-import-" + hex.EncodeToString(b)
}

func proImportBusy(p model.MailAccountProfile) bool {
	if p.ProWorkflowRunning || p.ProAuto.Status == "running" {
		return true
	}
	for _, v := range p.ProManualStages {
		if v.Status == "running" {
			return true
		}
	}
	return false
}

// Import each account plus every order atomically, encrypted with this
// installation's key. Reimporting the same snapshot cannot rewind progress.
func (s *Store) ImportProAccount(entry ProTransferAccount, exportedAt time.Time, overwrite bool, downstream model.ProDownstreamRef) (ProTransferResult, error) {
	if err := ValidateProTransfer(ProTransferFile{Format: ProTransferFormat, Version: ProTransferVersion, ExportedAt: exportedAt, Accounts: []ProTransferAccount{entry}}); err != nil {
		return ProTransferResult{Email: entry.Profile.Email}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ProTransferResult{Email: strings.ToLower(strings.TrimSpace(entry.Profile.Email))}
	payload, err := json.Marshal(entry)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(sum[:])
	// Do not mutate the caller's maps while normalizing interrupted stages.
	entry = ProTransferAccount{}
	if err = json.Unmarshal(payload, &entry); err != nil {
		return result, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var raw string
	exists := false
	var old model.MailAccountProfile
	err = tx.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", result.Email).Scan(&raw)
	if err == nil {
		exists = true
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return result, err
		}
		if old.ProMigration != nil && old.ProMigration.Fingerprint == fingerprint {
			result.Status = "skipped"
			result.Message = "相同快照已导入，保留当前进度"
			return result, nil
		}
		if !overwrite {
			result.Status = "skipped"
			result.Message = "同邮箱账号已存在"
			return result, nil
		}
		if old.ManagementScope != "pro" {
			return result, errors.New("同邮箱账号不在 Pro 管理中，请先移入 Pro 管理")
		}
		if old.VM2 != nil && (entry.Profile.VM2 == nil || old.VM2.AssignmentID != entry.Profile.VM2.AssignmentID) {
			return result, errors.New("不能覆盖不同的 vm2api 槽位绑定")
		}
		if old.VM2 != nil && old.VM2.DeletedAt != nil && entry.Profile.VM2.DeletedAt == nil {
			return result, errors.New("不能恢复已删除的 vm2api 槽位")
		}
		if proImportBusy(old) {
			return result, errors.New("账号正在执行任务，不能覆盖")
		}
		var restockActive bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_restock_tasks WHERE email=? AND status NOT IN ('ready','failed','cancelled'))`, result.Email).Scan(&restockActive); err != nil {
			return result, err
		}
		if restockActive {
			return result, errors.New("账号存在自动补货预留或在途任务，不能覆盖迁移快照")
		}
		if old.ProMigration != nil && exportedAt.Before(old.ProMigration.ExportedAt) {
			return result, errors.New("文件比已导入版本更旧，不能回退")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	var freeCount int
	if err = tx.QueryRow("SELECT count(*) FROM free_accounts WHERE lower(json_extract(profile,'$.email'))=?", result.Email).Scan(&freeCount); err != nil {
		return result, err
	}
	if freeCount > 0 {
		return result, errors.New("该账号仍在 Team 轮转列表中，不能覆盖为 Pro 迁移账号")
	}
	p := entry.Profile
	// Restock intents and encrypted target IDs belong to the source database.
	// Imports retain the ordinary downstream identity check, never a stale lock.
	p.ProRestockTaskID, p.ProRestockTargetID = "", ""
	if exists {
		p.ProRestockTaskID, p.ProRestockTargetID = old.ProRestockTaskID, old.ProRestockTargetID
	}
	if exists {
		p.ID = old.ID
	} else {
		p.ID = proTransferID()
	}
	if p.VM2 != nil {
		entry.VM2Target.Scope = "pro"
		raw, e := json.Marshal(entry.VM2Target)
		if e != nil {
			return result, e
		}
		h := hmac.New(sha256.New, s.key)
		_, _ = h.Write(raw)
		id := fmt.Sprintf("target-%x", h.Sum(nil))
		encrypted, e := s.encrypt(string(raw))
		if e != nil {
			return result, e
		}
		if _, e = tx.Exec("INSERT OR IGNORE INTO downstream_connections(id,encrypted_payload) VALUES(?,?)", id, encrypted); e != nil {
			return result, e
		}
		p.VM2.ConnectionID = id
		if p.VM2.Stage == "deleting" {
			p.VM2.Stage = "delete_failed"
			p.VM2.Error = "迁移时清理未确认，可重试"
		}
	}
	p.Email = result.Email
	p.ManagementScope = "pro"
	p.ProWorkflowRunning = false
	// Daily cohorts belong to the source instance's scheduler, not this import.
	p.ProDailyMerge = nil
	if p.ProMergeTrigger == "daily_ejh" {
		p.ProMergeTrigger = ""
	}
	if p.ProPostMergeGroups != nil && p.ProPostMergeGroups.Status == "running" {
		p.ProPostMergeGroups.Status = "pending"
		p.ProPostMergeGroups.UpdatedAt = time.Now()
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.ProManagedAt == nil {
		p.ProManagedAt = &now
	}
	if p.Label == "" {
		p.Label = p.Email
	}
	p.UpdatedAt = now
	notice := "已导入进度；停止来源任务后，点击继续迁移流程或使用单项按钮"
	teamID := entry.TargetTeamID
	if teamID == "" {
		teamID = p.TargetTeamID
	}
	p.TargetAdminID = ""
	if teamID != "" {
		var id string
		err = tx.QueryRow("SELECT id FROM admin_accounts WHERE json_extract(profile,'$.team_account_id')=? ORDER BY id LIMIT 1", teamID).Scan(&id)
		if err == nil {
			p.TargetAdminID = id
		} else if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		p.TargetTeamID = teamID
		if id == "" {
			notice += "；请先添加原空间对应的母号"
		}
	}
	if p.VM2 == nil && p.PushStatus == "completed" && !entry.Downstream.Matches(downstream) {
		notice += "；下游服务不同，请先推送到当前下游"
	}
	p.ProMigration = &model.ProMigrationState{ImportedAt: now, ExportedAt: exportedAt, Fingerprint: fingerprint, Paused: true, Downstream: entry.Downstream, TargetTeamID: teamID, Notice: notice}
	if p.ProAuto.ID != "" && p.ProAuto.Status != "completed" {
		p.ProAuto.Status = "interrupted"
		p.ProAuto.NextCheckAt = nil
		p.ProAuto.Error = "已迁移；原运行会话不能跨服务器恢复，请按已保存进度继续"
	}
	for step, status := range p.ProAuto.Steps {
		if status == "running" || status == "pending" {
			p.ProAuto.Steps[step] = "interrupted"
		}
	}
	for step, progress := range p.ProManualStages {
		if progress.Status == "running" {
			progress.Status = "interrupted"
			progress.Error = "已迁移，请核实原任务结果后继续"
			progress.UpdatedAt = now
			p.ProManualStages[step] = progress
		}
	}
	for _, status := range []*string{&p.InviteStatus, &p.AcceptStatus, &p.TransferStatus, &p.RemoveStatus} {
		if *status == "running" {
			if status == &p.TransferStatus {
				*status = "unknown"
				p.ProLastError = "迁移时合并正在执行，需核实远端结果后修正状态，禁止重复合并"
			} else {
				*status = "pending"
			}
		}
	}
	if p.PushStatus == "running" {
		p.PushStatus = "failed"
		p.ProLastError = "迁移时推送正在执行，请先核实下游结果"
	}
	if p.QuotaStatus == "running" {
		p.QuotaStatus = "pending"
	}
	// Reject conflicts rather than silently attach another account's payment.
	incoming := map[string]bool{}
	for _, o := range entry.Orders {
		incoming[o.Order.ID] = true
		var existingRaw, existingSealed string
		err = tx.QueryRow("SELECT profile,encrypted_snapshot FROM gptpay_orders WHERE id=?", o.Order.ID).Scan(&existingRaw, &existingSealed)
		if err == nil {
			var existing gptpay.Order
			if err = json.Unmarshal([]byte(existingRaw), &existing); err != nil {
				return result, err
			}
			plain, e := s.decrypt(existingSealed)
			if e != nil {
				return result, e
			}
			var oldSnapshot gptpay.Snapshot
			if e = json.Unmarshal([]byte(plain), &oldSnapshot); e != nil {
				return result, e
			}
			a, _ := json.Marshal(oldSnapshot)
			b, _ := json.Marshal(o.Snapshot)
			if !strings.EqualFold(existing.Email, p.Email) || string(a) != string(b) || existing.Remote.ID != "" && existing.Remote.ID != o.Order.Remote.ID {
				return result, errors.New("目标环境已有不一致的同编号订单，未覆盖")
			}
			if existing.UpdatedAt.After(o.Order.UpdatedAt) || (!existing.Active() && existing.Status != o.Order.Status) {
				return result, errors.New("目标环境订单进度较新，未覆盖")
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	rows, err := tx.Query("SELECT id FROM gptpay_orders WHERE email=?", p.Email)
	if err != nil {
		return result, err
	}
	extra := false
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		if !incoming[id] {
			extra = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	if extra {
		return result, errors.New("目标环境已有文件之外的订单，未覆盖账号")
	}
	c := entry.Credentials
	c.Email = p.Email
	p = mailProfileWithCredentials(p, c)
	sealed, err := s.encrypt(mustJSON(c))
	if err != nil {
		return result, err
	}
	rawBytes, err := json.Marshal(p)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(`INSERT INTO mail_accounts(id,email,label,group_name,profile,encrypted_credentials,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(email) DO UPDATE SET label=excluded.label,group_name=excluded.group_name,profile=excluded.profile,encrypted_credentials=excluded.encrypted_credentials,updated_at=excluded.updated_at`, p.ID, p.Email, p.Label, p.Group, string(rawBytes), sealed, formatTime(now))
	if err != nil {
		return result, errors.New("账号保存失败，未导入")
	}
	for _, o := range entry.Orders {
		sealed, err = s.encrypt(mustJSON(o.Snapshot))
		if err != nil {
			return result, err
		}
		rawBytes, err = json.Marshal(o.Order)
		if err != nil {
			return result, err
		}
		_, err = tx.Exec(`INSERT INTO gptpay_orders(id,email,profile,encrypted_snapshot,active,created_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET profile=excluded.profile,encrypted_snapshot=excluded.encrypted_snapshot,active=excluded.active`, o.Order.ID, p.Email, string(rawBytes), sealed, o.Order.Active(), formatTime(o.Order.CreatedAt))
		if err != nil {
			return result, errors.New("订单保存失败，账号及订单均未导入")
		}
		if _, err = tx.Exec(`INSERT INTO pro_card_usage VALUES(?,?,?,?) ON CONFLICT(order_id) DO UPDATE SET status=excluded.status`, o.Order.ID, proCardKey(o.Snapshot.Input.Number), p.Email, o.Order.Status); err != nil {
			return result, err
		}
		kind := o.CardType
		if kind == "" {
			kind = "other"
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO pro_card_types VALUES(?,?)`, proCardKey(o.Snapshot.Input.Number), kind); err != nil {
			return result, err
		}
		// Per-order release state prevents a later purchase inheriting old removal.
		released := o.OccupancyReleased != nil && *o.OccupancyReleased
		if o.OccupancyReleased == nil && len(entry.Orders) == 1 && (p.RemoveStatus == "completed" || p.RemoveStatus == "team_removed") {
			released = true
		}
		if released && o.Order.Status == "success" {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO pro_card_order_releases VALUES(?,?)`, o.Order.ID, formatTime(now)); err != nil {
				return result, err
			}
		}
		if kind == "nexovcc" {
			if err = importNexovccTransferState(tx, o, exportedAt, now); err != nil {
				return result, err
			}
		} else if v := o.CaseStateOverride; v != nil {
			if _, err = tx.Exec(`INSERT INTO ejh_case_states(order_id,state,reason,updated_at,revision) VALUES(?,?,?,?,?)
 ON CONFLICT(order_id) DO UPDATE SET state=excluded.state,reason=excluded.reason,updated_at=excluded.updated_at,revision=ejh_case_states.revision+1`, o.Order.ID, v.State, v.Reason, formatTime(v.UpdatedAt), v.Revision); err != nil {
				return result, err
			}
			if err = s.ejhPutTx(tx, "match_audit", EJHID("audit", "import-case-state:"+o.Order.ID+now.String()), map[string]any{"action": "import_case_state", "order_id": o.Order.ID, "email": p.Email, "after": v, "at": now}); err != nil {
				return result, err
			}
		}
		// Old files only carry removal-based releases; they cannot prove a return.
		if _, err = tx.Exec(`DELETE FROM ejh_received_returns WHERE trade_id=?`, "transfer:"+o.Order.ID); err != nil {
			return result, err
		}
		if o.ReturnConfirmed && kind == "ejh" && o.Order.Status == "success" {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO ejh_received_returns SELECT ?,?
 WHERE NOT EXISTS(SELECT 1 FROM ejh_return_links WHERE order_id=?)`, "transfer:"+o.Order.ID, o.Order.ID, o.Order.ID); err != nil {
				return result, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	result.Status = "imported"
	if exists {
		result.Status = "updated"
	}
	result.Message = notice
	return result, nil
}

func (s *Store) ProExportEmails(query, mergeState string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	query = strings.ToLower(strings.TrimSpace(query))
	mergeState = normalizeProMergeFilter(mergeState)
	rows, err := s.db.Query(proAccountsCTE+` SELECT email FROM accounts
		WHERE (?='' OR search_text LIKE ?) AND (?='' OR merge_state=?)
		ORDER BY email LIMIT 10001`, query, "%"+query+"%", mergeState, mergeState)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	emails := []string{}
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}
