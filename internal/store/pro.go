package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"chatgpt-space-merge/internal/model"
)

func normalizeProSettings(v *model.ProSettings) {
	groupNames := make(map[int64]string)
	for index, id := range v.PostMergeGroupIDs {
		if index < len(v.PostMergeGroupNames) && groupNames[id] == "" {
			groupNames[id] = strings.TrimSpace(v.PostMergeGroupNames[index])
		}
	}
	v.PostMergeGroupIDs = uniquePositiveInt64(v.PostMergeGroupIDs)
	v.PostMergeGroupNames = make([]string, len(v.PostMergeGroupIDs))
	for index, id := range v.PostMergeGroupIDs {
		v.PostMergeGroupNames[index] = groupNames[id]
	}
	if v.MaxUnmerged < 1 {
		v.MaxUnmerged = 5
	}
	if v.ScheduleIntervalSeconds < 10 {
		v.ScheduleIntervalSeconds = 120
	}
	v.Provider = strings.ToLower(strings.TrimSpace(v.Provider))
	if v.Provider != "cpa" && v.Provider != "vm2api" {
		v.Provider = "sub2"
	}
	if v.QuotaCheckIntervalSeconds < 10 {
		v.QuotaCheckIntervalSeconds = 120
	}
	v.DeletionCheckIntervalSeconds = int(v.DeletionCheckInterval() / time.Second)
	if v.QuotaUsedThreshold <= 0 || v.QuotaUsedThreshold > 100 || math.IsNaN(v.QuotaUsedThreshold) || math.IsInf(v.QuotaUsedThreshold, 0) {
		v.QuotaUsedThreshold = 100
	}
	if v.TargetSeatType != "prolite" {
		v.TargetSeatType = "default"
	}
	if v.RetryCount < 0 {
		v.RetryCount = 0
	}
	if v.RetryCount > 10 {
		v.RetryCount = 10
	}
	if v.RetryIntervalSeconds < 1 {
		v.RetryIntervalSeconds = 3
	}
	if v.Concurrency < 1 {
		v.Concurrency = 2
	}
	if v.Concurrency > 20 {
		v.Concurrency = 20
	}
	if v.MergeConcurrency < 1 {
		v.MergeConcurrency = 2
	}
	if v.MergeConcurrency > 20 {
		v.MergeConcurrency = 20
	}
	normalizeSub2Settings(&v.Sub2)
	v.Sub2.Provider = v.Provider
	if v.CPA.StatusCheckIntervalSeconds < 10 {
		v.CPA.StatusCheckIntervalSeconds = 120
	}
	if v.CPA.QuotaCheckIntervalSeconds < 10 {
		v.CPA.QuotaCheckIntervalSeconds = 120
	}
}

func (s *Store) ProSettings() (model.ProSettings, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := model.DefaultProSettings()
	var raw, encryptedSub2, encryptedCPA string
	if err := s.db.QueryRow("SELECT profile, encrypted_sub2_password, encrypted_cpa_key FROM pro_settings WHERE id=1").Scan(&raw, &encryptedSub2, &encryptedCPA); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v, "", "", nil
		}
		return v, "", "", err
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, "", "", err
	}
	normalizeProSettings(&v)
	subPassword, err := s.decryptOptional(encryptedSub2)
	if err != nil {
		return v, "", "", err
	}
	cpaKey, err := s.decryptOptional(encryptedCPA)
	if err != nil {
		return v, "", "", err
	}
	v.Sub2.PasswordPresent, v.CPA.KeyPresent = encryptedSub2 != "", encryptedCPA != ""
	var vmKey string
	if err := s.db.QueryRow("SELECT encrypted_key FROM pro_vm2api_secret WHERE id=1").Scan(&vmKey); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, "", "", err
	}
	v.VM2API.KeyPresent = vmKey != ""
	return v, subPassword, cpaKey, nil
}

func (s *Store) SaveProSettings(v model.ProSettings, subPassword, cpaKey string, vmKeys ...string) (model.ProSettings, error) {
	if err := v.ValidateDisposition(); err != nil {
		return v, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	normalizeProSettings(&v)
	var oldSub, oldCPA string
	_ = s.db.QueryRow("SELECT encrypted_sub2_password, encrypted_cpa_key FROM pro_settings WHERE id=1").Scan(&oldSub, &oldCPA)
	var err error
	if strings.TrimSpace(subPassword) != "" {
		oldSub, err = s.encrypt(strings.TrimSpace(subPassword))
		if err != nil {
			return v, err
		}
	}
	if strings.TrimSpace(cpaKey) != "" {
		oldCPA, err = s.encrypt(strings.TrimSpace(cpaKey))
		if err != nil {
			return v, err
		}
	}
	var vmKey string
	if err = s.db.QueryRow("SELECT encrypted_key FROM pro_vm2api_secret WHERE id=1").Scan(&vmKey); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	if len(vmKeys) > 0 && strings.TrimSpace(vmKeys[0]) != "" {
		vmKey, err = s.encrypt(strings.TrimSpace(vmKeys[0]))
		if err != nil {
			return v, err
		}
	}
	v.VM2API.KeyPresent = vmKey != ""
	v.Sub2.PasswordPresent, v.CPA.KeyPresent = oldSub != "", oldCPA != ""
	raw, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if err = syncProPushTimeoutTx(tx, v); err != nil {
		return v, err
	}
	if _, err = tx.Exec("INSERT INTO pro_vm2api_secret(id,encrypted_key) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET encrypted_key=excluded.encrypted_key", vmKey); err != nil {
		return v, err
	}
	_, err = tx.Exec(`INSERT INTO pro_settings(id,profile,encrypted_sub2_password,encrypted_cpa_key) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET profile=excluded.profile,encrypted_sub2_password=excluded.encrypted_sub2_password,encrypted_cpa_key=excluded.encrypted_cpa_key`, string(raw), oldSub, oldCPA)
	if err != nil {
		return v, err
	}
	if len(vmKeys) > 0 && strings.TrimSpace(vmKeys[0]) != "" {
		if err = s.rotateProVMSecretTx(tx, v.VM2API.URL, strings.TrimSpace(vmKeys[0])); err != nil {
			return v, err
		}
	}
	return v, tx.Commit()
}

func (s *Store) SaveProOAuthSession(session model.ProOAuthSession, verifier string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sealed, err := s.encrypt(verifier)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO pro_oauth_sessions(id,profile,encrypted_verifier,expires_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET profile=excluded.profile,encrypted_verifier=excluded.encrypted_verifier,expires_at=excluded.expires_at", session.ID, string(raw), sealed, formatTime(session.ExpiresAt))
	return err
}

func (s *Store) ProOAuthSession(id string) (model.ProOAuthSession, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec("DELETE FROM pro_oauth_sessions WHERE expires_at < ?", formatTime(time.Now()))
	var raw, encrypted string
	if err := s.db.QueryRow("SELECT profile,encrypted_verifier FROM pro_oauth_sessions WHERE id=?", strings.TrimSpace(id)).Scan(&raw, &encrypted); err != nil {
		return model.ProOAuthSession{}, "", errors.New("OAuth 会话不存在或已过期")
	}
	var session model.ProOAuthSession
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		return session, "", err
	}
	verifier, err := s.decrypt(encrypted)
	return session, verifier, err
}

func (s *Store) DeleteProOAuthSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM pro_oauth_sessions WHERE id=?", strings.TrimSpace(id))
	return err
}

func (s *Store) UpdateProAccount(email string, mutate func(*model.MailAccountProfile)) (model.MailAccountProfile, error) {
	return s.updateProAccount(email, time.Time{}, mutate)
}

// Check the UI version under the same lock as the business-state transaction.
func (s *Store) UpdateProAccountIfUnchanged(email string, expected time.Time, mutate func(*model.MailAccountProfile)) (model.MailAccountProfile, error) {
	return s.updateProAccount(email, expected, mutate)
}

func (s *Store) updateProAccount(email string, expected time.Time, mutate func(*model.MailAccountProfile)) (model.MailAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	var raw string
	if err := s.db.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw); err != nil {
		return model.MailAccountProfile{}, errors.New("Pro 账号不存在")
	}
	var p model.MailAccountProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	if normalizeMailManagementScope(p.ManagementScope) != "pro" {
		return p, errors.New("账号不在 Pro 管理中")
	}
	if !expected.IsZero() && !p.UpdatedAt.Equal(expected) {
		return p, ErrProStageConflict
	}
	wasConfirmed := p.TransferStatus == "completed" && p.ProMergeConfirmedSource != ""
	wasRemoved := p.RemoveStatus == "completed" || p.RemoveStatus == "team_removed"
	wasDeleted := p.DeletionAccepted()
	previousProvider, previousSub2ID := p.PushProvider, p.Sub2AccountID
	previousPushStatus := p.PushStatus
	// Preserve a historical end before a manual retry replaces its stage data.
	if p.ProPushStartedAt != nil && p.ProPushEndedAt == nil {
		p.ProPushEndedAt = p.ProPushEndTime()
	}
	wasEnding := p.ProWorkflowRunning || p.SpaceMergedOnce || p.DeletionSubmitted() ||
		(p.ProAuto.Status == "running" && (p.ProAuto.Stage == "merge" || p.ProAuto.Stage == "deactivate"))
	if mutate != nil {
		mutate(&p)
	}
	if previousPushStatus != "completed" && p.PushStatus == "completed" && p.ProPushStartedAt == nil {
		now := time.Now()
		p.ProPushStartedAt = &now
	}
	if p.PushProvider != previousProvider || p.Sub2AccountID != previousSub2ID || p.VM2 != nil {
		p.ClearProCostSnapshot()
	}
	p.UpdatedAt = time.Now()
	if p.DeletionSubmitted() {
		p.ReconcileProAutoDeletion(p.UpdatedAt)
	} else {
		p.ReconcileProAutoMerge(p.UpdatedAt)
	}
	p.ReconcileProQualityMerge()
	if p.ProPushEndTime() != nil || !wasEnding {
		p.FreezeProPushDuration(p.UpdatedAt)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE mail_accounts SET profile=?,updated_at=? WHERE email=?", string(encoded), formatTime(p.UpdatedAt), email); err != nil {
		return p, err
	}
	if err = s.recordEJHConsumptionTx(tx, p, p.UpdatedAt); err != nil {
		return p, err
	}
	if err = s.recordNexovccRevenueTx(tx, p); err != nil {
		return p, err
	}
	if (!wasRemoved && (p.RemoveStatus == "completed" || p.RemoveStatus == "team_removed")) || (!wasDeleted && p.DeletionAccepted()) {
		if err = releaseProCardOrdersTx(tx, email, formatTime(p.UpdatedAt)); err != nil {
			return p, err
		}
	}
	if !wasConfirmed && p.TransferStatus == "completed" && p.ProMergeConfirmedSource != "" {
		at := p.UpdatedAt
		if p.SpaceMergedAt != nil {
			at = *p.SpaceMergedAt
		}
		if err = ejhMarkRequestedTx(tx, p.Email, p.ProAuto.OrderID, p.ProMergeConfirmedSource, at); err != nil {
			return p, err
		}
	}
	if !wasDeleted && p.DeletionAccepted() {
		if err = ejhMarkRequestedTx(tx, p.Email, p.ProAuto.OrderID, "account_deactivation", *p.ProDeactivation.AcceptedAt); err != nil {
			return p, err
		}
	}
	return p, tx.Commit()
}

func (s *Store) RecoverProWorkflows() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := model.DefaultProSettings()
	var settingsRaw string
	if err := s.db.QueryRow("SELECT profile FROM pro_settings WHERE id=1").Scan(&settingsRaw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if settingsRaw != "" {
		if err := json.Unmarshal([]byte(settingsRaw), &settings); err != nil {
			return err
		}
	}
	rows, err := s.db.Query("SELECT email,profile FROM mail_accounts")
	if err != nil {
		return err
	}
	type item struct {
		email   string
		profile model.MailAccountProfile
	}
	var updates []item
	for rows.Next() {
		var email, raw string
		if rows.Scan(&email, &raw) != nil {
			continue
		}
		var p model.MailAccountProfile
		if json.Unmarshal([]byte(raw), &p) != nil || normalizeMailManagementScope(p.ManagementScope) != "pro" {
			continue
		}
		legacyCancelled := p.TransferStatus == "failed" && (strings.Contains(p.ProLastError, "context canceled") || strings.Contains(p.ProLastError, "context deadline exceeded"))
		groupsRunning := p.ProPostMergeGroups != nil && p.ProPostMergeGroups.Status == "running"
		// Normalize clocks saved by older versions without reviving completed
		// deletion workflows, even when downstream cleanup remains unfinished.
		deletionReconciled := false
		if p.DeletionConfirmed() {
			deletionReconciled = p.ReconcileProAutoDeletion(time.Now())
		}
		deletionNeedsRecovery := p.ProDeactivation != nil && (deletionReconciled || p.ProDeactivation.Status == "running" || p.DeletionNeedsContinuation() && p.ProDeactivation.NextCheckAt == nil)
		if !p.ProWorkflowRunning && !legacyCancelled && !groupsRunning && !deletionNeedsRecovery {
			continue
		}
		if legacyCancelled {
			p.TransferStatus = "unknown"
		}
		p.ProWorkflowRunning = false
		if d := p.ProDeactivation; d != nil && d.Status == "running" {
			d.Status = "interrupted"
			if d.SubmittedAt != nil {
				d.Status = "unknown"
			}
			if d.AcceptedAt != nil {
				d.Status = "queued"
			}
			d.Error = "服务重启，可续跑核验；已提交的删除不会重复发送"
			d.UpdatedAt = time.Now()
		}
		if d := p.ProDeactivation; d != nil && p.DeletionNeedsContinuation() {
			next := time.Now().Add(settings.DeletionCheckInterval())
			d.NextCheckAt = &next
		}
		if groupsRunning {
			p.ProPostMergeGroups.Status = "pending"
			p.ProPostMergeGroups.UpdatedAt = time.Now()
		}
		for _, status := range []*string{&p.InviteStatus, &p.AcceptStatus, &p.TransferStatus, &p.RemoveStatus} {
			if *status == "running" {
				if status == &p.TransferStatus {
					*status = "unknown"
				} else {
					*status = "pending"
				}
			}
		}
		p.ProLastError = "服务重启，流程将在下次检测或手动执行时续跑"
		if p.TransferStatus == "unknown" {
			p.ProLastError = "服务在合并途中重启，远端结果待确认；请核实合并结果后手动修正状态再续跑"
		}
		if p.DeletionConfirmed() {
			p.ProDeactivation.Status, p.ProDeactivation.Stage = "completed", "confirm"
			p.ProLastError = p.ProDeactivation.Error
		}
		p.UpdatedAt = time.Now()
		updates = append(updates, item{email: email, profile: p})
	}
	_ = rows.Close()
	for _, update := range updates {
		raw, _ := json.Marshal(update.profile)
		if _, err = s.db.Exec("UPDATE mail_accounts SET profile=?,updated_at=? WHERE email=?", string(raw), formatTime(update.profile.UpdatedAt), update.email); err != nil {
			return err
		}
	}
	return nil
}
