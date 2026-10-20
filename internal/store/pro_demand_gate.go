package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Bind permission to the saved monitoring connection and selected fallback
// accounts. Only a digest is retained; credentials never enter observations.
func restockDemandScope(q proCapacityQuery) (string, error) {
	var primary, secret, fallback, target string
	err := q.QueryRow(`SELECT coalesce((SELECT profile FROM pro_settings WHERE id=1),''),coalesce((SELECT encrypted_sub2_password FROM pro_settings WHERE id=1),''),coalesce((SELECT payload FROM pro_fallback_config WHERE id=1),''),coalesce((SELECT target_id FROM pro_fallback_config WHERE id=1),'')`).Scan(&primary, &secret, &fallback, &target)
	if err != nil {
		return "", err
	}
	p := model.DefaultProSettings()
	if primary != "" {
		if err = json.Unmarshal([]byte(primary), &p); err != nil {
			return "", err
		}
	}
	raw, _ := json.Marshal([]any{p.Provider, p.Sub2, secret, fallback, target})
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func (s *Store) ProRestockDemandScope() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return restockDemandScope(s.db)
}

func attachRestockDemand(q proCapacityQuery, c *restock.Config) error {
	if !c.HasDemandWindows() {
		return nil
	}
	var raw string
	err := q.QueryRow(`SELECT payload FROM pro_restock_demand_gate WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var gate restock.DemandGate
	if err = json.Unmarshal([]byte(raw), &gate); err != nil {
		return err
	}
	scope, err := restockDemandScope(q)
	if err != nil {
		return err
	}
	if gate.Scope == scope && gate.Version == c.Version {
		c.DemandGate = &gate
	}
	return nil
}

func (s *Store) SaveProRestockDemand(g restock.DemandGate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE pro_restock_config SET payload=payload WHERE id=1`); err != nil {
		return err
	}
	c, err := readRestockConfig(tx)
	if err != nil {
		return err
	}
	scope, err := restockDemandScope(tx)
	if err != nil {
		return err
	}
	if c.Version != g.Version || g.Scope != scope {
		return errors.New("监控或兜底配置已变化，等待重新采集")
	}
	_, err = tx.Exec(`INSERT INTO pro_restock_demand_gate VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, mustJSON(g))
	if err != nil {
		return err
	}
	return tx.Commit()
}
