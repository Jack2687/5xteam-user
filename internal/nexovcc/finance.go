package nexovcc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Recharge struct {
	ID       string    `json:"id"`
	Currency string    `json:"currency"`
	Amount   string    `json:"amount"`
	Received string    `json:"received"`
	Fee      string    `json:"fee"`
	Status   string    `json:"status"`
	At       time.Time `json:"at"`
}
type Trade struct {
	ID               string    `json:"id"`
	Serial           string    `json:"serial_no"`
	Last4            string    `json:"last4"`
	Authorization    string    `json:"authorization"`
	Currency         string    `json:"currency"`
	Amount           string    `json:"amount"`
	MerchantAmount   string    `json:"merchant_amount"`
	MerchantCurrency string    `json:"merchant_currency"`
	Merchant         string    `json:"merchant"`
	Type             string    `json:"type"`
	Status           string    `json:"status"`
	At               time.Time `json:"at"`
}
type Fund struct {
	ID       string    `json:"id"`
	Link     string    `json:"link"`
	Serial   string    `json:"serial_no"`
	Last4    string    `json:"last4"`
	Currency string    `json:"currency"`
	Kind     int       `json:"kind"`
	Amount   string    `json:"amount"`
	Balance  string    `json:"balance"`
	At       time.Time `json:"at"`
}
type Daily struct {
	Day      string            `json:"day"`
	Currency string            `json:"currency"`
	Values   map[string]string `json:"values"`
}
type Finance struct {
	Recharges []Recharge `json:"recharges"`
	Trades    []Trade    `json:"trades"`
	Funds     []Fund     `json:"funds"`
	Days      []Daily    `json:"days"`
	At        time.Time  `json:"at"`
	Error     string     `json:"error"`
}
type TradeGroup struct {
	ID               string    `json:"id"`
	Serial           string    `json:"serial_no"`
	Authorization    string    `json:"authorization"`
	Payment          string    `json:"payment"`
	Returned         string    `json:"returned"`
	Difference       string    `json:"difference"`
	Cost             string    `json:"cost"`
	Fees             string    `json:"fees"`
	MerchantAmount   string    `json:"merchant_amount"`
	MerchantCurrency string    `json:"merchant_currency"`
	FullyReturned    bool      `json:"fully_returned"`
	Settled          bool      `json:"settled"`
	Valid            bool      `json:"valid"`
	Problem          string    `json:"problem"`
	At               time.Time `json:"at"`
	ReturnedAt       time.Time `json:"returned_at"`
	TradeIDs         []string  `json:"trade_ids"`
}

func SignedMoney(n int64) string {
	if n < 0 {
		return "-" + Money(-n)
	}
	return Money(n)
}
func SignedCents(v string) (int64, error) {
	if strings.HasPrefix(v, "-") {
		n, e := Cents(v[1:])
		return -n, e
	}
	return Cents(v)
}
func Currency(v any) string {
	switch text(v) {
	case "3":
		return "USD"
	case "2":
		return "HKD"
	case "4":
		return "USDT"
	}
	return text(v)
}
func last4(v any) string {
	s := text(v)
	if len(s) > 4 {
		return s[len(s)-4:]
	}
	return s
}
func FinanceTime(v any) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, e := time.ParseInLocation(layout, text(v), time.FixedZone("CST", 8*3600)); e == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("环球流水时间格式无效")
}
func moneyField(m map[string]any, k string, signed bool) (string, error) {
	v := text(m[k])
	var n int64
	var e error
	if signed {
		n, e = SignedCents(v)
	} else {
		n, e = Cents(v)
	}
	if e != nil {
		return "", fmt.Errorf("环球流水 %s 金额无效", k)
	}
	return SignedMoney(n), nil
}

// Require total/page metadata for financial lists. A server-side page cap must
// not turn a partial page into a seemingly complete financial ledger.
func (c *Client) FinancePages(ctx context.Context, path string, body map[string]any) ([]map[string]any, error) {
	all := []map[string]any{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 1000; page++ {
		b := map[string]any{}
		for k, v := range body {
			b[k] = v
		}
		b["page"], b["pageSize"] = page, 100
		data, e := c.Do(ctx, path, b)
		if e != nil {
			return nil, e
		}
		m, ok := data.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("环球财务分页结构无效")
		}
		n, e := strconv.Atoi(text(m["total"]))
		if e != nil || n < 0 {
			return nil, fmt.Errorf("环球财务分页缺少总数")
		}
		if total >= 0 && total != n {
			return nil, fmt.Errorf("环球流水在分页期间发生变化，请重新同步")
		}
		total = n
		actual, e := strconv.Atoi(text(m["page"]))
		if e != nil || actual != page {
			return nil, fmt.Errorf("环球财务分页页码不符")
		}
		rows, e := List(data)
		if e != nil {
			return nil, e
		}
		raw, _ := json.Marshal(rows)
		key := string(raw)
		if len(rows) > 0 && seen[key] {
			return nil, fmt.Errorf("环球财务分页重复")
		}
		seen[key] = true
		all = append(all, rows...)
		if len(all) == total {
			return all, nil
		}
		if len(all) > total || len(rows) == 0 {
			return nil, fmt.Errorf("环球财务分页不完整")
		}
	}
	return nil, fmt.Errorf("环球财务记录超过同步上限，已保留上次完整数据")
}

func (c *Client) ReadFinance(ctx context.Context, progress func(string)) (Finance, error) {
	f := Finance{Recharges: []Recharge{}, Trades: []Trade{}, Funds: []Fund{}, Days: []Daily{}}
	until := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02") + " 23:59:59"
	body := map[string]any{"time_min": "2020-01-01 00:00:00", "time_max": until}
	progress("分页同步充值记录")
	rows, e := c.FinancePages(ctx, "/api/findLogRecharge", body)
	if e != nil {
		return f, e
	}
	ids := map[string]bool{}
	for _, m := range rows {
		r := Recharge{ID: text(m["order_no"]), Currency: Currency(m["money_id"]), Status: text(m["status"])}
		if r.ID == "" || ids[r.ID] {
			return f, fmt.Errorf("环球充值流水缺失或重复")
		}
		ids[r.ID] = true
		if r.Amount, e = moneyField(m, "amount", false); e != nil {
			return f, e
		}
		if r.Received, e = moneyField(m, "real_amount", false); e != nil {
			return f, e
		}
		if r.Fee, e = moneyField(m, "fee", false); e != nil {
			return f, e
		}
		if r.At, e = FinanceTime(m["created_at"]); e != nil {
			return f, e
		}
		f.Recharges = append(f.Recharges, r)
	}
	progress("分页同步卡片消费与回款")
	rows, e = c.FinancePages(ctx, "/api/findLogCardTrades", body)
	if e != nil {
		return f, e
	}
	ids = map[string]bool{}
	for _, m := range rows {
		r := Trade{ID: text(m["log_no"]), Serial: text(m["serial_no"]), Last4: last4(m["card_no"]), Authorization: text(m["business_token"]), Currency: Currency(m["money_id"]), MerchantCurrency: text(m["merchant_currency"]), Merchant: text(m["merchant_address"]), Type: text(m["trade_type"]), Status: text(m["status"])}
		if r.ID == "" || r.Serial == "" || ids[r.ID] {
			return f, fmt.Errorf("环球交易流水缺失或重复")
		}
		ids[r.ID] = true
		if r.Amount, e = moneyField(m, "trade_amount", false); e != nil {
			return f, e
		}
		r.MerchantAmount = text(m["merchant_amount"])
		if r.At, e = FinanceTime(m["created_at"]); e != nil {
			return f, e
		}
		f.Trades = append(f.Trades, r)
	}
	progress("分页同步实际费用与资金流水")
	fundsBody := map[string]any{"time_min": body["time_min"], "time_max": body["time_max"], "balance_type_id": 2, "is_default": 0}
	rows, e = c.FinancePages(ctx, "/api/findLogWebFunds", fundsBody)
	if e != nil {
		return f, e
	}
	ids = map[string]bool{}
	occurrences := map[string]int{}
	for _, m := range rows {
		r := Fund{Link: text(m["link_log_no"]), Serial: text(m["serial_no"]), Last4: last4(m["card_no"]), Currency: Currency(m["money_id"])}
		if r.Kind, e = strconv.Atoi(text(m["change_type_id"])); e != nil {
			return f, fmt.Errorf("环球资金变动类型无效")
		}
		if r.Amount, e = moneyField(m, "amount", true); e != nil {
			return f, e
		}
		if r.Balance, e = moneyField(m, "after_balance", true); e != nil {
			return f, e
		}
		if r.At, e = FinanceTime(m["created_at"]); e != nil {
			return f, e
		}
		// The observed funds endpoint has no primary ID. Preserve identical rows
		// within a snapshot, and replace complete snapshots instead of appending.
		raw, _ := json.Marshal(r)
		key := fmt.Sprintf("%x", sha256.Sum256(raw))
		occurrences[key]++
		r.ID = fmt.Sprintf("%s-%d", key, occurrences[key])
		f.Funds = append(f.Funds, r)
	}
	progress("同步平台每日汇总")
	rows, e = c.FinancePages(ctx, "/api/findLogReportCardDay", map[string]any{"money_id": 3, "date_min": nil, "date_max": nil})
	if e != nil {
		return f, e
	}
	for _, m := range rows {
		r := Daily{Day: text(m["count_date"]), Currency: Currency(m["money_id"]), Values: map[string]string{}}
		if _, e = FinanceTime(r.Day); e != nil {
			return f, e
		}
		for _, k := range []string{"income_day", "income_fee_day", "income_count", "open_card_day", "open_card_count", "un_auth_day", "un_auth_count", "consume_day", "consume_count", "cancel_day", "cancel_count", "refund_day", "refund_count", "complete_day", "complete_count", "refund_fee_day", "foreign_fee_day", "card_day", "cancel_fee"} {
			if m[k] != nil {
				r.Values[k] = text(m[k])
			}
		}
		f.Days = append(f.Days, r)
	}
	f.At = time.Now()
	return f, nil
}

// Recharge fees are calculated separately at 1%; transfers are not costs.
// Rebates reduce the original fee instead of becoming additional income.
func IsFee(kind int) bool {
	switch kind {
	case 3003, 2003, 2007, 2008, 2009, 4005, 4006, 8888, 5001, 5003, 5004, 5008, 5009, 5010:
		return true
	}
	return false
}
func OpeningFee(kind int) bool { return kind == 3003 || kind == 5001 }
func Groups(f Finance) []TradeGroup {
	buckets := map[string][]Trade{}
	feesByTrade := map[string][]Fund{}
	for _, fee := range f.Funds {
		if IsFee(fee.Kind) && !OpeningFee(fee.Kind) && fee.Link != "" {
			key := fee.Serial + "|" + fee.Link
			feesByTrade[key] = append(feesByTrade[key], fee)
		}
	}
	for _, t := range f.Trades {
		key := t.Serial + "|" + t.Authorization
		if t.Authorization == "" {
			key += "|" + t.ID
		}
		buckets[key] = append(buckets[key], t)
	}
	out := []TradeGroup{}
	for key, rows := range buckets {
		g := TradeGroup{ID: fmt.Sprintf("%x", sha256.Sum256([]byte(key))), Serial: rows[0].Serial, Authorization: rows[0].Authorization, Valid: true, TradeIDs: []string{}}
		var paid, returned, settled, merchantPaid, merchantReturned, fees int64
		payments := 0
		completeCount := 0
		var completedAt time.Time
		fullReturnEvidence := true
		for _, r := range rows {
			g.TradeIDs = append(g.TradeIDs, r.ID)
			n, e := Cents(r.Amount)
			if e != nil || r.Currency != "USD" {
				g.Valid = false
				g.Problem = "金额或币种待核实"
				continue
			}
			switch r.Status {
			case "PENDING":
				payments++
				paid += n
				g.At = r.At
				g.MerchantCurrency = r.MerchantCurrency
				g.MerchantAmount = r.MerchantAmount
				merchantPaid, e = Cents(r.MerchantAmount)
				if e != nil {
					fullReturnEvidence = false
				}
			case "REVERSED", "REFUNDED":
				returned += n
				if r.At.After(g.ReturnedAt) {
					g.ReturnedAt = r.At
				}
				m, err := Cents(r.MerchantAmount)
				if err != nil {
					fullReturnEvidence = false
				} else {
					merchantReturned += m
				}
			case "COMPLETE":
				settled += n
				completeCount++
				completedAt = r.At
				g.Settled = true
				if g.At.IsZero() {
					g.At = r.At
					g.MerchantCurrency = r.MerchantCurrency
					g.MerchantAmount = r.MerchantAmount
				}
			case "DECLINED", "EXPIRED":
			default:
				g.Valid = false
				g.Problem = "未知交易状态"
			}
		}
		// After a full merchant reversal the supplier can settle only the
		// remaining FX difference (92.33 paid, 92.28 returned, 0.05 COMPLETE).
		// That is the same loss already represented by paid-returned, not a
		// replacement 0.05 payment and not an additional expense.
		returnCurrencyMatches := true
		for _, r := range rows {
			if (r.Status == "REVERSED" || r.Status == "REFUNDED") && r.MerchantCurrency != g.MerchantCurrency {
				returnCurrencyMatches = false
			}
		}
		residualSettlement := payments == 1 && completeCount == 1 && returned > 0 && settled > 0 &&
			paid == returned+settled && fullReturnEvidence && returnCurrencyMatches &&
			merchantPaid > 0 && merchantPaid == merchantReturned &&
			!g.ReturnedAt.IsZero() && !completedAt.Before(g.ReturnedAt)
		if completeCount == 1 && !residualSettlement {
			paid = settled
			for _, r := range rows {
				if r.Status == "COMPLETE" {
					g.MerchantAmount, g.MerchantCurrency = r.MerchantAmount, r.MerchantCurrency
					var err error
					merchantPaid, err = Cents(r.MerchantAmount)
					if err != nil {
						fullReturnEvidence = false
					}
				}
			}
		}
		if payments > 1 || completeCount > 1 {
			g.Valid = false
			g.Problem = "同授权码多笔付款，待核实"
		}
		if g.Authorization == "" {
			g.Valid = false
			g.Problem = "缺少授权码"
		}
		for _, r := range rows {
			if (r.Status == "REVERSED" || r.Status == "REFUNDED") && r.MerchantCurrency != g.MerchantCurrency {
				fullReturnEvidence = false
			}
		}
		g.FullyReturned = paid > 0 && returned > 0 && ((fullReturnEvidence && merchantPaid > 0 && merchantPaid == merchantReturned) || returned == paid)
		if returned > 0 && paid == 0 {
			g.Valid = false
			g.Problem = "缺少原付款记录"
		}
		if !g.At.IsZero() && !g.ReturnedAt.IsZero() && g.ReturnedAt.Before(g.At) {
			g.Valid = false
			g.Problem = "回款早于付款"
		}
		for _, id := range g.TradeIDs {
			for _, fee := range feesByTrade[g.Serial+"|"+id] {
				n, e := SignedCents(fee.Amount)
				if e != nil || fee.Currency != "USD" {
					g.Valid = false
					g.Problem = "费用币种待核实"
				} else {
					fees -= n
				}
			}
		}
		if g.Settled && returned > settled && !g.FullyReturned {
			g.Valid = false
			g.Problem = "退款超过结算金额"
		}
		cost := fees
		if !g.Valid {
			g.FullyReturned = false
		}
		if g.Valid && g.FullyReturned {
			cost += paid - returned
		} else if g.Valid && g.Settled {
			cost += settled - returned
		}
		g.Payment, g.Returned, g.Difference, g.Fees, g.Cost = Money(paid), Money(returned), SignedMoney(paid-returned), SignedMoney(fees), SignedMoney(cost)
		if !g.Valid {
			g.FullyReturned = false
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
