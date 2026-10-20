package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func ResolveProPurchase(p model.ProSettings, pay gptpay.Settings, c restock.Config, now time.Time) (model.ProSettings, gptpay.Settings, error) {
	p.PurchaseRoute = nil
	w, matched := p.PurchaseWindow(now)
	provider := p.AutoCardProvider()
	if matched {
		provider = w.CardProvider
	}
	p.CardProvider = c.EffectiveCardProvider(now, provider)
	if !matched {
		return p, pay, nil
	}
	id := p.PlanTemplate(p.CardProvider, w.PlanCode)
	if id == "" {
		return p, pay, fmt.Errorf("分时开通缺少 %s 的 %s 模板，请在银行卡与补卡中设置", p.CardProvider, w.PlanCode)
	}
	pay.PlanCode, pay.CreditQuantity = w.PlanCode, 0
	r := &model.ProPurchaseRoute{CardProvider: p.CardProvider, TemplateID: id, PlanCode: w.PlanCode, PaymentProvider: pay.Provider, Country: pay.Country}
	if len(w.ExtraSub2GroupIDs) > 0 && p.Provider != "sub2" {
		return p, pay, fmt.Errorf("分时开通的额外推送分组仅支持 Sub2，请检查推送设置")
	}
	if p.Provider == "sub2" {
		r.Sub2GroupIDs = model.PurchaseSub2Groups(p.Sub2.GroupIDs, w.ExtraSub2GroupIDs)
	}
	return p.WithPurchaseRoute(r), pay, nil
}

func readProPurchase(q proCapacityQuery, c restock.Config, now time.Time) (*model.ProPurchaseRoute, error) {
	p := model.DefaultProSettings()
	var raw string
	err := q.QueryRow(`SELECT profile FROM pro_settings WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	if _, matched := p.PurchaseWindow(now); !matched {
		return nil, nil
	}
	var pay gptpay.Settings
	if err = q.QueryRow(`SELECT profile FROM gptpay_settings WHERE id=1`).Scan(&raw); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &pay); err != nil {
		return nil, err
	}
	p, _, err = ResolveProPurchase(p, pay, c, now)
	return p.PurchaseRoute, err
}

func SameProPurchase(a, b *model.ProPurchaseRoute) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.CardProvider == b.CardProvider && a.TemplateID == b.TemplateID &&
		a.PlanCode == b.PlanCode && a.PaymentProvider == b.PaymentProvider &&
		a.Country == b.Country && a.CreditQuantity == b.CreditQuantity &&
		slices.Equal(a.Sub2GroupIDs, b.Sub2GroupIDs)
}

type proTemplateQuery interface {
	Query(string, ...any) (*sql.Rows, error)
}

func (s *Store) proTemplateCards(q proTemplateQuery, provider, template, plan string) (map[string]bool, error) {
	out := map[string]bool{}
	if provider == "nexovcc" {
		rows, err := q.Query(`SELECT json_extract(profile,'$.card_id') FROM nexovcc_operations WHERE json_extract(profile,'$.action')='open' AND json_extract(profile,'$.template_id')=? AND coalesce(json_extract(profile,'$.plan_code'),'') IN ('',?) AND coalesce(json_extract(profile,'$.card_id'),'')<>''`, template, plan)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return nil, err
			}
			out[id] = true
		}
		return out, rows.Err()
	}
	rows, err := q.Query(`SELECT sealed FROM ejh_records WHERE kind='operation'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sealed string
		if err = rows.Scan(&sealed); err != nil {
			return nil, err
		}
		raw, e := s.decrypt(sealed)
		if e != nil {
			return nil, e
		}
		var op ejh.Operation
		if e = json.Unmarshal([]byte(raw), &op); e != nil {
			return nil, e
		}
		if op.Method == "open" && op.TemplateID == template && (op.PlanCode == "" || op.PlanCode == plan) {
			if op.CardID != "" {
				out[op.CardID] = true
			}
			if op.OrderNo != "" {
				out[EJHID("card", op.OrderNo)] = true
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) ProTemplateCards(provider, template, plan string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proTemplateCards(s.db, provider, template, plan)
}
