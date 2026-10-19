package store

import (
	"chatgpt-space-merge/internal/ejh"
	"database/sql"
	"time"
)

// Migration carries account/order state, not merchant credentials or a new
// payment. Card and monetary snapshots are refreshed by the destination issuer.
func exportNexovccTransferStates(tx *sql.Tx, orders []ProTransferOrder) error {
	for i := range orders {
		o := &orders[i]
		if o.CardType != "nexovcc" {
			continue
		}
		o.CaseStateOverride = nil
		v := ejh.CaseStateOverride{}
		var at string
		err := tx.QueryRow(`SELECT state,reason,updated_at,revision FROM nexovcc_case_states WHERE order_id=?`, o.Order.ID).Scan(&v.State, &v.Reason, &at, &v.Revision)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			v.UpdatedAt, _ = time.Parse(time.RFC3339Nano, at)
			if v.State == "auto" {
				v.State = ""
			}
			o.CaseStateOverride = &v
		}
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_case_matches WHERE order_id=? AND returned=1)`, o.Order.ID).Scan(&o.ReturnConfirmed); err != nil {
			return err
		}
		err = tx.QueryRow(`SELECT released_at FROM pro_card_order_releases WHERE order_id=?`, o.Order.ID).Scan(&at)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			t, e := time.Parse(time.RFC3339Nano, at)
			if e != nil {
				return e
			}
			o.ReturnRequestedAt = &t
		}
	}
	return nil
}

func importNexovccTransferState(tx *sql.Tx, o ProTransferOrder, exportedAt, now time.Time) error {
	if o.ReturnRequestedAt != nil && o.Order.Status == "success" {
		if _, err := tx.Exec(`INSERT INTO pro_card_order_releases(order_id,released_at) VALUES(?,?) ON CONFLICT(order_id) DO UPDATE SET released_at=excluded.released_at`, o.Order.ID, formatTime(*o.ReturnRequestedAt)); err != nil {
			return err
		}
	}
	v := o.CaseStateOverride
	if o.ReturnConfirmed && o.Order.Status == "success" && (v == nil || v.State == "") {
		// Preserve confirmed release without inventing amounts or local trade IDs.
		v = &ejh.CaseStateOverride{State: "returned", Reason: "迁移源已通过环球流水确认回款；金额请同步当前卡台流水核对", UpdatedAt: exportedAt, Revision: 1}
	}
	if v == nil {
		return nil
	}
	state := v.State
	if state == "" {
		state = "auto"
	}
	if _, err := tx.Exec(`INSERT INTO nexovcc_case_states(order_id,state,reason,updated_at,revision) VALUES(?,?,?,?,?) ON CONFLICT(order_id) DO UPDATE SET state=excluded.state,reason=excluded.reason,updated_at=excluded.updated_at,revision=nexovcc_case_states.revision+1`, o.Order.ID, state, v.Reason, formatTime(v.UpdatedAt), v.Revision); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{"action": "import_case_state", "order_id": o.Order.ID, "after": v, "at": now}))
	return err
}
