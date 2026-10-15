package store

import (
	"chapt-space-user/internal/gptpay"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) issuerForPhysicalCard(q proCapacityQuery, key string) (*gptpay.Card, error) {
	var sealed string
	e := q.QueryRow(`SELECT r.sealed FROM ejh_card_links l JOIN ejh_records r ON r.kind='card' AND r.id=l.card_id WHERE l.card_key=?`, key).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	raw, e := s.decrypt(sealed)
	if e != nil {
		return nil, e
	}
	var record struct {
		Error  string `json:"error"`
		Remote struct {
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
	return &gptpay.Card{Source: "ejh", IssuerOrderNo: record.Remote.OrderNo, IssuerStatus: record.Remote.Status, IssuerLocked: record.Remote.Locked == "1" || record.Error != "" || pending, IssuerEnd: record.Remote.End}, nil
}
func (s *Store) validateIssuerCard(q proCapacityQuery, key string) error {
	c, e := s.issuerForPhysicalCard(q, key)
	if e != nil {
		return e
	}
	if c != nil && !issuerCardUsable(*c) {
		return fmt.Errorf("卡台卡片未激活、冻结、过期或状态不可用")
	}
	return nil
}

func issuerCardUsable(c gptpay.Card) bool {
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
