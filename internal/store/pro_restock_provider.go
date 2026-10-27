package store

import (
	"chatgpt-space-merge/internal/cardplatform"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func restockCardProvider(q proCapacityQuery, c restock.Config, now time.Time) (string, bool, error) {
	p := model.DefaultProSettings()
	var raw string
	err := q.QueryRow(`SELECT profile FROM pro_settings WHERE id=1`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return "", false, err
		}
	}
	provider := p.AutoCardProvider()
	_, routed := p.PurchaseWindow(now)
	if w, ok := p.PurchaseWindow(now); ok {
		provider = w.CardProvider
	}
	route, err := readProPurchase(q, c, now)
	if err != nil {
		return "", false, err
	}
	routed = routed || route != nil
	return c.EffectiveCardProvider(now, provider), routed || c.CardSchedule(now).Provider != "", nil
}

// Only a claimed intent may override global card settings. The durable choice
// survives schedule boundaries; reserved intents are re-evaluated at claim.
func restockDispatchTask(q proCapacityQuery, id string) (restock.Task, error) {
	var t restock.Task
	var raw string
	err := q.QueryRow(`SELECT t.payload FROM pro_restock_tasks t JOIN mail_accounts m ON m.email=t.email WHERE t.id=? AND json_extract(m.profile,'$.pro_restock_task_id')=t.id`, id).Scan(&raw)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(raw), &t); err != nil {
		return t, err
	}
	if !t.Active() || t.Status == "reserved" || (t.CardProvider != "ejh" && t.CardProvider != "nexovcc") {
		return t, fmt.Errorf("补货任务尚未领取或卡台信息无效")
	}
	return t, nil
}

func (s *Store) ProRestockDispatchTask(id string) (restock.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return restockDispatchTask(s.db, id)
}

func RestockCardMatches(card gptpay.Card, provider string, strict bool) bool {
	if !cardplatform.IsOAI(card.Platform) {
		return false
	}
	if provider == "nexovcc" {
		return card.Source == "nexovcc" || card.CardType == "nexovcc"
	}
	if strict {
		return card.Source == "ejh" || card.CardType == "ejh"
	}
	return card.Source != "nexovcc" && card.CardType != "nexovcc"
}
