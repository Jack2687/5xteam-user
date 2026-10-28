package ejh

import (
	"fmt"
	"math/big"

	"chatgpt-space-merge/internal/cardplatform"
)

// Keep automatic destruction narrower than manual administration. Inactive,
// restricted, failed-cancel and unknown cards require an operator's review.
func AutoCancelNormalCard(card CardRecord) bool {
	return !card.NormalPending && cardplatform.IsOAI(card.Platform) && card.Error == "" &&
		card.Remote.Locked != "1" && (card.Remote.Status == "1" || card.Remote.Status == "3")
}

// Validate actual issuer evidence, not a manual status, OAI confirmation or a
// released reservation. Compare principal in transaction currency when present
// (FX can change the card-currency amount); fees are accounted separately.
func ValidateAutoCancelReturns(card CardRecord, cases []Case, trades []TradeRecord) error {
	byID := map[string]TradeRecord{}
	for _, t := range trades {
		byID[t.ID] = t
	}
	payments := map[string]TradeRecord{}
	for _, t := range PaymentTrades(trades) {
		payments[chargeKey(t)] = t
	}
	used := map[string]bool{}
	returns := map[string]bool{}
	count := 0
	for _, c := range cases {
		if c.CardID != card.ID && c.EJHCardID != card.ID {
			continue
		}
		if c.PaymentStatus == "failed" && c.State == "unopened" {
			continue
		}
		if c.PaymentStatus != "success" || c.State != "returned" || c.ProDead != nil {
			return fmt.Errorf("关联账号尚未全部回款完成")
		}
		original, ok := byID[c.OriginalTradeID]
		p, matched := payments[chargeKey(original)]
		if !ok || !matched || p.CardID != card.ID || p.Trade.OrderNo != card.Remote.OrderNo || used[chargeKey(p)] {
			return fmt.Errorf("原付款流水缺失、重复或归属未确认")
		}
		amount, currency := p.Trade.CardAmount, p.Trade.CardCurrency
		transactionCurrency := Positive(p.Trade.TradeAmount) == nil && ValidCurrency(p.Trade.TradeCurrency)
		if transactionCurrency {
			amount, currency = p.Trade.TradeAmount, p.Trade.TradeCurrency
		}
		want, err := Money(amount)
		if err != nil || want.Sign() <= 0 {
			return fmt.Errorf("原付款金额无效")
		}
		sum := new(big.Rat)
		for _, r := range trades {
			if r.OrderID != c.ID {
				continue
			}
			if !r.Confirmed || !ReturnReceived(r.Trade) || r.CardID != card.ID ||
				r.Trade.OrderNo != card.Remote.OrderNo || r.OriginalTradeID != c.OriginalTradeID ||
				r.Currency != r.Trade.CardCurrency || Positive(r.Amount) != nil {
				return fmt.Errorf("回款流水未确认到账或关联不一致")
			}
			confirmed, _ := Money(r.Amount)
			actual, _ := Money(r.Trade.CardAmount)
			if confirmed.Cmp(actual) != 0 {
				return fmt.Errorf("回款确认金额与卡台不一致")
			}
			if r.Trade.Type != "2" && (Positive(r.Trade.BillAmount) != nil || !ValidCurrency(r.Trade.BillCurrency)) {
				return fmt.Errorf("退款结算金额尚未到账")
			}
			v, unit := r.Trade.CardAmount, r.Trade.CardCurrency
			if transactionCurrency {
				v, unit = r.Trade.TradeAmount, r.Trade.TradeCurrency
			}
			n, err := Money(v)
			if err != nil || n.Sign() <= 0 || unit != currency || returns[chargeKey(r)] {
				return fmt.Errorf("回款金额、币种或流水去重待核实")
			}
			returns[chargeKey(r)] = true
			sum.Add(sum, n)
		}
		if sum.Cmp(want) != 0 {
			return fmt.Errorf("回款本金未全部到账或金额待核实")
		}
		used[chargeKey(p)] = true
		count++
	}
	if count == 0 || len(used) != len(payments) {
		return fmt.Errorf("存在未关联付款或没有已回款账号")
	}
	for _, t := range trades {
		if t.Trade.IsReturn() && (!ReturnReceived(t.Trade) || !t.Confirmed || !returns[chargeKey(t)]) {
			return fmt.Errorf("存在未到账或未核对的回款流水")
		}
		if (t.Trade.Type == "1" || t.Trade.Type == "4") && !ChargeUsable(t.Trade) {
			return fmt.Errorf("存在待核实的付款流水")
		}
	}
	return nil
}
