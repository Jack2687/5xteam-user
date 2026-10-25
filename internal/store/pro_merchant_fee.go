package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

type ProMerchantFee struct {
	RequestID string    `json:"request_id"`
	OrderID   string    `json:"order_id"`
	Email     string    `json:"email"`
	CardID    string    `json:"card_id"`
	Issuer    string    `json:"issuer"`
	Amount    string    `json:"amount"`
	At        time.Time `json:"at"`
}

func merchantFeeAmount(value string) (string, error) {
	if value == "" {
		value = "0.00"
	}
	n, err := nexovcc.Cents(value)
	if err != nil || n > 100000000 {
		return "", fmt.Errorf("API 成功收费须为 0～1000000 USD，最多两位小数")
	}
	return nexovcc.Money(n), nil
}

// Called while holding the store lock, before the first submission. Subsequent
// setting edits cannot change the fee or card of an already submitted request.
func (s *Store) pinProMerchantFee(m *model.ProMerchant, p model.MailAccountProfile) error {
	var value string
	err := s.db.QueryRow(`SELECT coalesce(json_extract(profile,'$.merchant_success_fee_usd'),'0.00') FROM pro_settings WHERE id=1`).Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	m.SuccessFeeUSD, err = merchantFeeAmount(value)
	if err != nil {
		return err
	}
	var key string
	err = s.db.QueryRow(`SELECT o.id,u.card_key FROM pro_card_usage u JOIN gptpay_orders o ON o.id=u.order_id WHERE u.email=? COLLATE NOCASE AND u.status='success' ORDER BY (o.id=?) DESC,o.created_at DESC,o.id DESC LIMIT 1`, p.Email, p.ProAuto.OrderID).Scan(&m.FeeOrderID, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = s.db.QueryRow(`SELECT card_id,'ejh' FROM ejh_card_links WHERE card_key=? UNION ALL SELECT id,'nexovcc' FROM nexovcc_cards WHERE card_key=? LIMIT 1`, key, key).Scan(&m.FeeCardID, &m.FeeIssuer)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// A financial receipt survives account deletion. The request key makes final
// success polling, retries and repeated profile saves exactly-once accounting.
func recordProMerchantFeeTx(tx *sql.Tx, p model.MailAccountProfile) error {
	if !p.MerchantCompleted() {
		return nil
	}
	m := p.ProMerchant
	value, err := merchantFeeAmount(m.SuccessFeeUSD)
	if err != nil {
		return err
	}
	n, _ := nexovcc.Cents(value)
	_, err = tx.Exec(`INSERT INTO pro_merchant_fees(request_id,order_id,email,card_id,issuer,amount_cents,at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(request_id) DO NOTHING`, m.RequestID, m.FeeOrderID, p.Email, m.FeeCardID, m.FeeIssuer, n, formatTime(*m.CompletedAt))
	return err
}

func (s *Store) ProMerchantFees(issuer string) ([]ProMerchantFee, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT request_id,order_id,email,card_id,issuer,amount_cents,at FROM pro_merchant_fees WHERE issuer=? ORDER BY at DESC,request_id`, issuer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProMerchantFee{}
	for rows.Next() {
		var fee ProMerchantFee
		var cents int64
		var at string
		if err = rows.Scan(&fee.RequestID, &fee.OrderID, &fee.Email, &fee.CardID, &fee.Issuer, &cents, &at); err != nil {
			return nil, err
		}
		fee.At, _ = time.Parse(time.RFC3339Nano, at)
		fee.Amount = nexovcc.Money(cents)
		items = append(items, fee)
	}
	return items, rows.Err()
}

func proMerchantFeesByOrder(q ejhCaseQuery) (map[string]string, error) {
	rows, err := q.Query(`SELECT order_id,sum(amount_cents) FROM pro_merchant_fees WHERE order_id<>'' GROUP BY order_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id string
		var n int64
		if err = rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = nexovcc.Money(n)
	}
	return out, rows.Err()
}
