package nexovcc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
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
	FundsAt   map[string]time.Time `json:"funds_at,omitempty"`
	AccountAt time.Time            `json:"account_at,omitempty"`
	CardAt    map[string]time.Time `json:"card_at,omitempty"`
	Recharges []Recharge           `json:"recharges"`
	Trades    []Trade              `json:"trades"`
	Funds     []Fund               `json:"funds"`
	Days      []Daily              `json:"days"`
	At        time.Time            `json:"at"`
	Error     string               `json:"error"`
}
type TradeGroup struct {
	NonPro            bool         `json:"non_pro,omitempty"`
	PurposeRevision   int          `json:"purpose_revision"`
	OtherExpense      *TradeReview `json:"other_expense,omitempty"`
	ID                string       `json:"id"`
	Serial            string       `json:"serial_no"`
	Authorization     string       `json:"authorization"`
	Payment           string       `json:"payment"`
	Returned          string       `json:"returned"`
	Difference        string       `json:"difference"`
	Cost              string       `json:"cost"`
	Fees              string       `json:"fees"`
	MerchantAmount    string       `json:"merchant_amount"`
	MerchantCurrency  string       `json:"merchant_currency"`
	FullyReturned     bool         `json:"fully_returned"`
	Settled           bool         `json:"settled"`
	Valid             bool         `json:"valid"`
	Problem           string       `json:"problem"`
	At                time.Time    `json:"at"`
	ReturnedAt        time.Time    `json:"returned_at"`
	TradeIDs          []string     `json:"trade_ids"`
	FeeTradeIDs       []string     `json:"fee_trade_ids,omitempty"`
	RelatedGroupID    string       `json:"related_group_id,omitempty"`
	RelatedSettlement string       `json:"related_settlement,omitempty"`
}

// A proven, settled charge from a zero-dollar reversal authorization is a
// final expense attached to the returned payment, not another refundable sale.
func (g TradeGroup) ReturnResolved() bool {
	return g.Valid && (g.FullyReturned || g.RelatedGroupID != "" && g.Settled)
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

type financeQuery struct {
	cardNumber, serial string
	accountOnly        bool
	summaryOnly        bool
	since              time.Time
}

// ReadFinance is the explicit historical import reader. Routine refreshes use
// ReadCardFinance and ReadAccountFinance below instead.
func (c *Client) ReadFinance(ctx context.Context, progress func(string)) (Finance, error) {
	return c.readFinance(ctx, financeQuery{}, progress)
}

func (c *Client) ReadCardFinance(ctx context.Context, number, serial string) (Finance, error) {
	if !ValidFinanceCardNumber(number) || serial == "" {
		return Finance{}, fmt.Errorf("缺少完整卡号，未发起全账户流水查询")
	}
	return c.readFinance(ctx, financeQuery{cardNumber: number, serial: serial}, func(string) {})
}

// Receipt confirmation needs only the current card's funds, not its payment
// history, account recharges, or daily statistics.
func (c *Client) ReadCardFunds(ctx context.Context, number, serial string) (Finance, error) {
	if !ValidFinanceCardNumber(number) || serial == "" {
		return Finance{}, fmt.Errorf("缺少完整卡号，未发起全账户流水查询")
	}
	return c.readFinance(ctx, financeQuery{cardNumber: number, serial: serial, accountOnly: true}, func(string) {})
}

func ValidFinanceCardNumber(number string) bool {
	if len(number) < 12 || len(number) > 19 {
		return false
	}
	for _, r := range number {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (c *Client) ReadAccountFinance(ctx context.Context, since time.Time) (Finance, error) {
	return c.readFinance(ctx, financeQuery{accountOnly: true, since: since}, func(string) {})
}

// Routine refreshes get trades and funds by card. Account-wide reads are
// restricted to recharge records and daily totals, which have no card filter.
func (c *Client) ReadAccountSummary(ctx context.Context, since time.Time) (Finance, error) {
	return c.readFinance(ctx, financeQuery{accountOnly: true, summaryOnly: true, since: since}, func(string) {})
}

func (c *Client) readFinance(ctx context.Context, q financeQuery, progress func(string)) (Finance, error) {
	f := Finance{Recharges: []Recharge{}, Trades: []Trade{}, Funds: []Fund{}, Days: []Daily{}}
	until := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02") + " 23:59:59"
	body := map[string]any{"time_min": "2020-01-01 00:00:00", "time_max": until}
	if !q.since.IsZero() {
		body["time_min"] = q.since.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")
	}
	if q.cardNumber != "" {
		body["card_no"] = q.cardNumber
	}
	var rows []map[string]any
	var e error
	ids := map[string]bool{}
	if q.cardNumber == "" {
		progress("分页同步充值记录")
		rows, e = c.FinancePages(ctx, "/api/findLogRecharge", body)
		if e != nil {
			return f, e
		}
		ids = map[string]bool{}
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
	}
	if !q.accountOnly {
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
			if q.serial != "" && r.Serial != q.serial {
				return f, fmt.Errorf("卡片消费接口返回了其他卡片，已保留原流水")
			}
			f.Trades = append(f.Trades, r)
		}
	}
	if !q.summaryOnly {
		progress("分页同步实际费用与资金流水")
		fundsBody := map[string]any{"time_min": body["time_min"], "time_max": body["time_max"], "balance_type_id": 2, "is_default": 0}
		if q.cardNumber != "" {
			fundsBody["card_no"] = q.cardNumber
		}
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
			if !q.since.IsZero() && r.At.Before(q.since) {
				return f, fmt.Errorf("财务明细返回了时间范围之外的数据，已保留原流水")
			}
			// The observed funds endpoint has no primary ID. Preserve identical rows
			// within each complete card/window snapshot. The ID stays stable across scopes.
			raw, _ := json.Marshal(r)
			key := fmt.Sprintf("%x", sha256.Sum256(raw))
			occurrences[key]++
			r.ID = fmt.Sprintf("%s-%d", key, occurrences[key])
			if q.serial != "" && r.Serial != q.serial {
				return f, fmt.Errorf("财务明细接口返回了其他卡片，已保留原流水")
			}
			f.Funds = append(f.Funds, r)
		}
	}
	if q.cardNumber == "" {
		progress("同步平台每日汇总")
		daysBody := map[string]any{"money_id": 3, "date_min": nil, "date_max": nil}
		if !q.since.IsZero() {
			daysBody["date_min"] = q.since.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
			daysBody["date_max"] = until[:10]
		}
		rows, e = c.FinancePages(ctx, "/api/findLogReportCardDay", daysBody)
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
	}
	f.At = time.Now()
	return f, nil
}

// Recharge fees are calculated separately at 1%; transfers are not costs.
// Rebates reduce the original fee instead of becoming additional income.
func IsFee(kind int) bool {
	switch kind {
	case 3003, 3004, 3005, 2003, 2007, 2008, 2009, 4005, 4006, 8888, 5001, 5003, 5004, 5008, 5009, 5010:
		return true
	}
	return false
}
func OpeningFee(kind int) bool { return kind == 3003 || kind == 5001 }
func ServiceFee(kind int) bool { return kind == 3004 || kind == 3005 }

func tradeBucketKey(t Trade) string {
	key := t.Serial + "|" + t.Authorization
	if t.Authorization == "" {
		key += "|" + t.ID
	}
	return key
}

func normalizedMerchant(value string) string {
	value = strings.Join(strings.Fields(strings.ToUpper(value)), " ")
	// Issuer descriptors change between authorization, settlement and refund.
	// Only collapse observed ChatGPT subscription descriptors; arbitrary brands,
	// other OpenAI products and unknown suffixes retain exact-name matching.
	compact := strings.ReplaceAll(value, " ", "")
	const subscription = "OPENAI*CHATGPTSUBSCR"
	if strings.HasPrefix(compact, subscription) {
		suffix := strings.TrimPrefix(compact, subscription)
		if suffix == "" || suffix == "OPENAI.COMCAUS" || suffix == "SANFRANCISCOCAUS" || merchantPhoneSuffix.MatchString(suffix) {
			return subscription
		}
	}
	return value
}

var merchantPhoneSuffix = regexp.MustCompile(`^\+[0-9]{7,15}(CAUS)?$`)

func standaloneRefund(rows []Trade) (Trade, bool) {
	if len(rows) != 1 {
		return Trade{}, false
	}
	refund := rows[0]
	amount, err := Cents(refund.Amount)
	return refund, err == nil && amount > 0 && refund.Status == "REFUNDED" && refund.Currency == "USD" &&
		refund.Authorization != "" && refund.Serial != "" && !refund.At.IsZero()
}

func standalonePayment(rows []Trade) (Trade, int64, bool) {
	if len(rows) < 1 || len(rows) > 2 {
		return Trade{}, 0, false
	}
	payment := rows[0]
	if len(rows) == 2 {
		pending, settled := rows[0], rows[1]
		if pending.Status == "COMPLETE" {
			pending, settled = settled, pending
		}
		if pending.Status != "PENDING" || settled.Status != "COMPLETE" ||
			pending.Serial != settled.Serial || pending.Authorization != settled.Authorization ||
			pending.Currency != settled.Currency || pending.MerchantCurrency != settled.MerchantCurrency ||
			pending.At.IsZero() || settled.At.Before(pending.At) {
			return Trade{}, 0, false
		}
		// Authorization and settlement describe one payment, not two purchases.
		payment = settled
	}
	amount, err := Cents(payment.Amount)
	if err != nil || amount <= 0 || payment.Currency != "USD" || (payment.Status != "PENDING" && payment.Status != "COMPLETE") ||
		payment.Authorization == "" || payment.Serial == "" || payment.At.IsZero() {
		return Trade{}, 0, false
	}
	return payment, amount, true
}

// Unique completed refunds may use a separate authorization. Keep the supplier
// ledger intact, and only merge a refund when one earlier same-card payment has
// the same merchant/currencies and enough unreturned principal.
func mergeCrossAuthorizationRefunds(buckets map[string][]Trade) map[string]bool {
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	proposed := map[string]string{}
	targetClaims := map[string]int{}
	for _, sourceKey := range keys {
		refund, ok := standaloneRefund(buckets[sourceKey])
		if !ok || normalizedMerchant(refund.Merchant) == "" || refund.MerchantCurrency == "" {
			continue
		}
		refundAmount, err := Cents(refund.Amount)
		if err != nil {
			continue
		}
		refundMerchantAmount, merchantErr := Cents(refund.MerchantAmount)
		if merchantErr != nil || refundMerchantAmount <= 0 {
			continue
		}
		candidates := []string{}
		for _, targetKey := range keys {
			if sourceKey == targetKey {
				continue
			}
			payment, paid, ok := standalonePayment(buckets[targetKey])
			if !ok || payment.Serial != refund.Serial || !payment.At.Before(refund.At) ||
				payment.Currency != refund.Currency || payment.MerchantCurrency != refund.MerchantCurrency ||
				normalizedMerchant(payment.Merchant) != normalizedMerchant(refund.Merchant) {
				continue
			}
			paidMerchant, err := Cents(payment.MerchantAmount)
			if err != nil || paidMerchant <= 0 || refundAmount > paid || refundMerchantAmount > paidMerchant {
				continue
			}
			candidates = append(candidates, targetKey)
		}
		if len(candidates) == 1 {
			proposed[sourceKey] = candidates[0]
			targetClaims[candidates[0]]++
		}
	}
	completed := map[string]bool{}
	for source, target := range proposed {
		if targetClaims[target] != 1 {
			continue
		}
		buckets[target] = append(buckets[target], buckets[source]...)
		delete(buckets, source)
		completed[target] = true
	}
	return completed
}

func Groups(f Finance) []TradeGroup {
	return groupsWithCompletedRefunds(f, nil)
}

func groupsWithCompletedRefunds(f Finance, manualRefunds map[string]bool) []TradeGroup {
	buckets := map[string][]Trade{}
	feesByTrade := map[string][]Fund{}
	for _, fee := range f.Funds {
		if IsFee(fee.Kind) && !OpeningFee(fee.Kind) && !ServiceFee(fee.Kind) && fee.Link != "" {
			key := fee.Serial + "|" + fee.Link
			feesByTrade[key] = append(feesByTrade[key], fee)
		}
	}
	for _, t := range f.Trades {
		key := tradeBucketKey(t)
		buckets[key] = append(buckets[key], t)
	}
	autoRefunds := mergeCrossAuthorizationRefunds(buckets)
	// A reversal can produce a separate zero-dollar authorization with its own
	// transaction fee. Attribute fees only when the same-card return at the exact
	// timestamp is unique; keep both original transaction records unchanged.
	feeTrades := map[string][]string{}
	attributed := map[string]bool{}
	related := map[string]string{}
	for key, rows := range buckets {
		if len(rows) < 1 || len(rows) > 2 {
			continue
		}
		zero := rows[0]
		if len(rows) == 2 && zero.Status == "COMPLETE" {
			zero = rows[1]
		}
		if zero.Status != "PENDING" || zero.At.IsZero() {
			continue
		}
		amount, err := Cents(zero.Amount)
		if err != nil || amount != 0 || len(feesByTrade[zero.Serial+"|"+zero.ID]) == 0 {
			continue
		}
		if len(rows) == 2 {
			settled := rows[1]
			if settled.ID == zero.ID {
				settled = rows[0]
			}
			n, err := Cents(settled.Amount)
			merchantAmount, me := Cents(settled.MerchantAmount)
			zeroMerchant, ze := Cents(zero.MerchantAmount)
			if settled.Status != "COMPLETE" || err != nil || n <= 0 || n > 100 || me != nil || ze != nil || merchantAmount != zeroMerchant ||
				settled.Currency != zero.Currency || settled.MerchantCurrency != zero.MerchantCurrency || settled.At.Before(zero.At) ||
				normalizedMerchant(settled.Merchant) != normalizedMerchant(zero.Merchant) {
				continue
			}
		}
		candidates := map[string]bool{}
		for other, trades := range buckets {
			if other == key {
				continue
			}
			// Reversal descriptions may be prefixed/truncated by the issuer.
			// Compare the zero authorization with the original payment merchant.
			merchant := normalizedMerchant(zero.Merchant)
			sameMerchant := false
			for _, paid := range trades {
				n, err := Cents(paid.Amount)
				if err == nil && n > 0 && (paid.Status == "PENDING" || paid.Status == "COMPLETE") && paid.Authorization != "" &&
					merchant != "" && normalizedMerchant(paid.Merchant) == merchant {
					sameMerchant = true
				}
			}
			if !sameMerchant {
				continue
			}
			for _, r := range trades {
				returned, err := Cents(r.Amount)
				if err == nil && returned > 0 && (r.Status == "REVERSED" || r.Status == "REFUNDED") &&
					r.Serial == zero.Serial && r.Currency == zero.Currency && r.At.Equal(zero.At) &&
					r.MerchantCurrency == zero.MerchantCurrency {
					candidates[other] = true
				}
			}
		}
		if len(candidates) == 1 {
			for target := range candidates {
				for _, r := range rows {
					feeTrades[target] = append(feeTrades[target], r.ID)
					attributed[r.Serial+"|"+r.ID] = true
				}
				related[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = fmt.Sprintf("%x", sha256.Sum256([]byte(target)))
			}
		}
	}
	out := []TradeGroup{}
	for key, rows := range buckets {
		g := TradeGroup{ID: fmt.Sprintf("%x", sha256.Sum256([]byte(key))), Serial: rows[0].Serial, Authorization: rows[0].Authorization, Valid: true, TradeIDs: []string{}}
		g.FeeTradeIDs = feeTrades[key]
		sort.Strings(g.FeeTradeIDs)
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
		// The issuer rounds the residual settlement independently. Permit one
		// cent only with a full merchant-currency reversal and later settlement;
		// the actual paid-returned difference remains the cost, never rounded away.
		residualDelta := paid - returned - settled
		residualSettlement := payments == 1 && completeCount == 1 && returned > 0 && settled > 0 && paid > returned &&
			residualDelta >= -1 && residualDelta <= 1 && fullReturnEvidence && returnCurrencyMatches &&
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
		// A uniquely linked REFUNDED event confirms the refund flow completed;
		// any amount the issuer did not return remains visible as card loss.
		g.FullyReturned = g.FullyReturned || autoRefunds[key] || manualRefunds[g.ID]
		if returned > 0 && paid == 0 {
			g.Valid = false
			g.Problem = "缺少原付款记录"
		}
		if !g.At.IsZero() && !g.ReturnedAt.IsZero() && g.ReturnedAt.Before(g.At) {
			g.Valid = false
			g.Problem = "回款早于付款"
		}
		feeIDs := append([]string{}, g.FeeTradeIDs...)
		for _, id := range g.TradeIDs {
			if !attributed[g.Serial+"|"+id] {
				feeIDs = append(feeIDs, id)
			}
		}
		for _, id := range feeIDs {
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
	byID := map[string]int{}
	for i := range out {
		byID[out[i].ID] = i
	}
	for i := range out {
		target, ok := byID[related[out[i].ID]]
		if !ok || !out[i].Valid || !out[target].Valid || !out[target].FullyReturned {
			continue
		}
		out[i].RelatedGroupID = out[target].ID
		if out[i].Settled {
			amount, _ := SignedCents(out[i].Difference)
			extra, _ := SignedCents(out[target].RelatedSettlement)
			cost, _ := SignedCents(out[target].Cost)
			out[target].RelatedSettlement = SignedMoney(extra + amount)
			out[target].Cost = SignedMoney(cost + amount)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
