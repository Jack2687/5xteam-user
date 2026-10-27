package store

import (
	"chatgpt-space-merge/internal/cardplatform"
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) issuerForPhysicalCard(q proCapacityQuery, key string) (*gptpay.Card, error) {
	card, err := s.issuerSnapshotForPhysicalCard(q, key)
	if err != nil || card == nil {
		return card, err
	}
	// Resolve the current merchant switch from the physical binding, including
	// duplicate/manual aliases. A card classification alone is not a binding.
	table := "ejh_settings"
	if card.Source == "nexovcc" {
		table = "nexovcc_settings"
	}
	var enabled bool
	if err = q.QueryRow(`SELECT COALESCE((SELECT json_extract(profile,'$.enabled') FROM ` + table + ` WHERE id=1),0)`).Scan(&enabled); err != nil {
		return nil, err
	}
	card.IssuerDisabled = !enabled
	return card, nil
}

func (s *Store) issuerSnapshotForPhysicalCard(q proCapacityQuery, key string) (*gptpay.Card, error) {
	var sealed string
	e := q.QueryRow(`SELECT r.sealed FROM ejh_card_links l JOIN ejh_records r ON r.kind='card' AND r.id=l.card_id WHERE l.card_key=?`, key).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return s.nexovccIssuer(q, key)
	}
	if e != nil {
		return nil, e
	}
	raw, e := s.decrypt(sealed)
	if e != nil {
		return nil, e
	}
	var record struct {
		Platform     string `json:"platform"`
		TemplateName string `json:"template_name,omitempty"`
		TemplateID   string `json:"template_id"`
		Error        string `json:"error"`
		Remote       struct {
			OrderNo string `json:"orderNo"`
			Status  string `json:"status"`
			Locked  string `json:"isLocked"`
			End     string `json:"endTime"`
		} `json:"remote"`
	}
	if e = json.Unmarshal([]byte(raw), &record); e != nil {
		return nil, e
	}
	var pending bool
	if e = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM ejh_card_links l JOIN ejh_operation_claims c ON c.scope='card:'||l.card_id WHERE l.card_key=?)`, key).Scan(&pending); e != nil {
		return nil, e
	}
	card := &gptpay.Card{Platform: record.Platform, TemplateID: record.TemplateID, TemplateName: record.TemplateName, Source: "ejh", IssuerOrderNo: record.Remote.OrderNo, IssuerStatus: record.Remote.Status, IssuerLocked: record.Remote.Locked == "1" || record.Error != "" || pending, IssuerEnd: record.Remote.End}
	card.IssuerFrozen = record.Remote.Locked == "1" || record.Remote.Status == "5"
	if pending {
		var operationSealed string
		if e = q.QueryRow(`SELECT r.sealed FROM ejh_card_links l JOIN ejh_operation_claims c ON c.scope='card:'||l.card_id JOIN ejh_records r ON r.kind='operation' AND r.id=c.operation_id WHERE l.card_key=?`, key).Scan(&operationSealed); e != nil {
			return nil, e
		}
		plain, err := s.decrypt(operationSealed)
		if err != nil {
			return nil, err
		}
		var op ejh.Operation
		if err = json.Unmarshal([]byte(plain), &op); err != nil {
			return nil, err
		}
		if op.Method == "cancel" {
			card.IssuerCancelStatus = op.Status
		}
	}
	return card, nil
}
func (s *Store) validateIssuerCard(q proCapacityQuery, key string) error {
	if dead, err := proCardDead(q, key); err != nil {
		return err
	} else if dead {
		return fmt.Errorf("该卡关联死号，已永久禁用新的开通；仍可查询、提现或销卡")
	}
	c, e := s.issuerForPhysicalCard(q, key)
	if e != nil {
		return e
	}
	if c != nil && !cardplatform.IsOAI(c.Platform) {
		return fmt.Errorf("Pro 只能使用 OAI 银行卡")
	}
	if c != nil && c.IssuerDisabled {
		name := "EJH"
		if c.Source == "nexovcc" {
			name = "环球"
		}
		return fmt.Errorf("%s 卡台已停用，不能用于新的 Pro 开通", name)
	}
	if c != nil && !issuerCardUsable(*c) {
		return fmt.Errorf("卡台卡片未激活、冻结、过期或状态不可用")
	}
	return nil
}

func issuerCardUsable(c gptpay.Card) bool {
	if c.IssuerDisabled || !cardplatform.IsOAI(c.Platform) {
		return false
	}
	if c.Source == "nexovcc" {
		return !c.IssuerLocked && c.IssuerStatus == "1"
	}
	if c.Source != "ejh" {
		return true
	}
	if c.IssuerLocked || (c.IssuerStatus != "1" && c.IssuerStatus != "3") {
		return false
	}
	if c.IssuerEnd != "" {
		end, e := time.ParseInLocation("2006-01-02 15:04:05", c.IssuerEnd, time.FixedZone("CST", 8*3600))
		if e != nil || !end.After(time.Now()) {
			return false
		}
	}
	return true
}
