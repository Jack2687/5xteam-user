package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/nexovcc"
)

// Opening operations own platform and template snapshots. Never infer the
// platform of an existing card from an editable template or an upstream status.
func (s *Store) setEJHCardOrigin(card *ejh.CardRecord, explicit ...*ejh.Operation) error {
	var op *ejh.Operation
	if len(explicit) > 0 && explicit[0] != nil && explicit[0].Method == "open" {
		op = explicit[0]
	} else {
		ops, err := EJHRecords[ejh.Operation](s, "operation")
		if err != nil {
			return err
		}
		for i := range ops {
			candidate := &ops[i]
			if candidate.Method == "open" && (candidate.CardID == card.ID || candidate.OrderNo != "" && candidate.OrderNo == card.Remote.OrderNo || candidate.Result != nil && candidate.Result.OrderNo == card.Remote.OrderNo) {
				op = candidate
				break
			}
		}
	}
	if card.Platform == "" {
		card.Platform = "oai"
	}
	if op == nil {
		return nil
	}
	card.Platform, card.TemplateID, card.TemplateName = op.Platform, op.TemplateID, op.TemplateName
	if card.Platform == "" {
		card.Platform = "oai"
	}
	if card.TemplateName == "" && card.TemplateID != "" {
		cfg, _, err := s.EJHSettings()
		if err != nil {
			return err
		}
		for _, t := range cfg.Templates {
			if t.ID == card.TemplateID {
				card.TemplateName = t.Name
				break
			}
		}
	}
	return nil
}

func setNexovccCardOrigin(q proCapacityQuery, card *nexovcc.Card) error {
	var raw string
	err := q.QueryRow(`SELECT profile FROM nexovcc_operations WHERE json_extract(profile,'$.action')='open' AND (json_extract(profile,'$.card_id')=? OR json_extract(profile,'$.serial_no')=? OR id=nullif(?,'')) ORDER BY json_extract(profile,'$.created_at') LIMIT 1`, card.ID, card.Serial, card.Remark).Scan(&raw)
	if card.Platform == "" {
		card.Platform = "oai"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var op nexovcc.Operation
	if err = json.Unmarshal([]byte(raw), &op); err != nil {
		return err
	}
	card.Platform, card.TemplateID, card.TemplateName = op.Platform, op.TemplateID, op.TemplateName
	if card.Platform == "" {
		card.Platform = "oai"
	}
	if card.TemplateName == "" && card.TemplateID != "" {
		err = q.QueryRow(`SELECT profile FROM nexovcc_settings WHERE id=1`).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var cfg nexovcc.Settings
		if err = json.Unmarshal([]byte(raw), &cfg); err != nil {
			return err
		}
		for _, t := range cfg.Templates {
			if t.ID == card.TemplateID {
				card.TemplateName = t.Name
				break
			}
		}
	}
	return nil
}
