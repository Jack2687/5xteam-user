package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatgpt-space-merge/internal/model"
)

type oaiReturnOrder struct {
	ID, Email, State string
	CreatedAt        time.Time
}

// Read the same local accounting evidence used by both card pages. No issuer
// or Help request is made. A refund can confirm Help, never the reverse.
func (s *Store) SyncProOAIRefundConfirmations() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT profile FROM mail_accounts WHERE json_extract(profile,'$.management_scope')='pro'`)
	if err != nil {
		return nil, err
	}
	var accounts []model.MailAccountProfile
	for rows.Next() {
		var raw string
		var p model.MailAccountProfile
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &p)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		if len(p.ProOAIConfirmations) > 0 || len(p.OAIAvailableModes()) > 0 {
			accounts = append(accounts, p)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(accounts) == 0 {
		return nil, err
	}
	cfg := model.DefaultProSettings()
	var configRaw string
	if err = tx.QueryRow("SELECT profile FROM pro_settings WHERE id=1").Scan(&configRaw); err == nil {
		if err = json.Unmarshal([]byte(configRaw), &cfg); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	cfg.OAIConfirmation = cfg.OAIConfirmation.Normalized()
	orders := map[string]oaiReturnOrder{}
	ejhRows, err := s.ejhCasesQuery(tx, nil)
	if err != nil {
		return nil, err
	}
	for _, c := range ejhRows {
		if c.CardType == "ejh" && c.PaymentStatus == "success" {
			orders[c.ID] = oaiReturnOrder{c.ID, strings.ToLower(c.Email), c.State, c.CreatedAt}
		}
	}
	nexRows, err := s.nexovccCasesQuery(tx)
	if err != nil {
		return nil, err
	}
	for _, c := range nexRows {
		if c.PaymentStatus == "success" {
			orders[c.ID] = oaiReturnOrder{c.ID, strings.ToLower(c.Email), c.State, c.CreatedAt}
		}
	}
	var changed []string
	now := time.Now()
	for _, p := range accounts {
		before := mustJSON(p.ProOAIConfirmations)
		for _, mode := range model.ProDispositionModes {
			v := p.ProOAIConfirmations[mode]
			if v == nil && !p.OAIConfirmationPrerequisite(mode) {
				continue
			}
			// A genuine Help confirmation remains valid regardless of accounting
			// corrections. Only an inference from a returned order is reversible.
			if v != nil && v.ConfirmedAt != nil && v.ConfirmationSource != "card_return" {
				continue
			}
			id := p.ProAuto.OrderID
			if v != nil && v.OrderID != "" {
				id = v.OrderID
			}
			order, found := orders[id]
			if id == "" {
				for _, candidate := range orders {
					if candidate.Email != strings.ToLower(p.Email) {
						continue
					}
					if v != nil && !v.CompletedAt.IsZero() && candidate.CreatedAt.After(v.CompletedAt) {
						continue
					}
					if !found || candidate.CreatedAt.After(order.CreatedAt) {
						order, found = candidate, true
					}
				}
			}
			if !found || order.Email != strings.ToLower(p.Email) {
				continue
			}
			if v != nil && !v.CompletedAt.IsZero() && order.CreatedAt.After(v.CompletedAt) {
				continue
			}
			if order.State != "returned" {
				if v != nil && v.ConfirmationSource == "card_return" {
					v.Status, v.ConfirmationSource, v.ConfirmedAt = "review", "", nil
					v.Error = "回款状态已调整，等待重新确认"
					v.CheckedAt = &now
					v.Steps, v.StepTimes = nil, nil
					v.SentAt, v.NextAttemptAt = nil, nil
					v.UserMessageID, v.MessageID = "", ""
					rule := cfg.OAIConfirmation[mode]
					v.RetryIntervalSeconds, v.MaxAttempts = rule.RetryIntervalSeconds, rule.MaxAttempts
					v.Generation++
					v.Step("confirm", "review", now)
				}
				continue
			}
			if v != nil && v.ConfirmationSource == "card_return" && v.ConfirmedAt != nil {
				continue
			}
			if v == nil {
				v = model.NewProOAIConfirmation(mode, cfg.OAIConfirmation[mode])
				v.CompletedAt = now
				if p.ProOAIConfirmations == nil {
					p.ProOAIConfirmations = map[string]*model.ProOAIConfirmation{}
				}
				p.ProOAIConfirmations[mode] = v
			}
			v.OrderID, v.Status, v.ConfirmationSource = order.ID, "confirmed", "card_return"
			v.ConfirmedAt = &now
			v.NotBefore, v.NextAttemptAt = nil, nil
			v.Error = ""
			v.Generation++
			for _, stage := range []string{"wait", "login", "authorize", "send", "reply"} {
				if v.Steps[stage] != "completed" {
					v.Step(stage, "skipped", now)
				}
			}
			v.Step("confirm", "confirmed", now)
		}
		if before == mustJSON(p.ProOAIConfirmations) {
			continue
		}
		p.UpdatedAt = now
		if _, err = tx.Exec(`UPDATE mail_accounts SET profile=?,updated_at=? WHERE email=?`, mustJSON(p), formatTime(now), p.Email); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO pro_oai_confirmation_accounts(email,profile) VALUES(?,?) ON CONFLICT(email) DO UPDATE SET profile=excluded.profile`, p.Email, mustJSON(p.ProOAIConfirmations)); err != nil {
			return nil, err
		}
		changed = append(changed, p.Email)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}
