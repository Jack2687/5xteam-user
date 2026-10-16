package ejh

import (
	"reflect"
	"testing"
	"time"
)

func autoTime(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}
func autoCase(id, at string) Case {
	opened := autoTime(at)
	requested := opened.Add(time.Hour)
	return Case{ID: id, Email: id + "@example.com", CardID: "card", CardType: "ejh", PaymentStatus: "success", CreatedAt: opened.Add(-time.Minute), OpenedAt: &opened, RequestedAt: &requested}
}
func autoTrade(id, kind, code, at string) TradeRecord {
	return TradeRecord{ID: id, CardID: "card", Trade: Trade{OrderNo: "issuer-card", TradeNo: id, Type: kind, Status: "2", AuthCode: code, CardAmount: "5794.64", CardCurrency: "PHP", TradeTime: at, Merchant: "OPENAI"}}
}
func TestAutoReconcileIssuerTimesAndClearing(t *testing.T) {
	cases := []Case{autoCase("a", "2026-10-15T17:24:19+08:00"), autoCase("b", "2026-10-15T21:36:26+08:00")}
	a := autoTrade("a-pay", "1", "EPUBOZ", "2026-10-15 17:24:02")
	a.Trade.Status = "1"
	a.Trade.CreatedAt = "2026-10-15 17:24:02"
	b := autoTrade("b-pay", "1", "259562", "2026-10-15 13:36:23")
	b.Trade.Status = "1"
	b.Trade.CreatedAt = "2026-10-15 21:40:34"
	clear := autoTrade("b-clear", "4", "259562", "2026-10-16T10:00:00+08:00")
	trades := []TradeRecord{clear, b, a}
	now := autoTime("2026-10-16T11:00:00+08:00")
	AutoReconcile(cases, trades, now)
	if cases[0].OriginalTradeID != a.ID || cases[1].OriginalTradeID != clear.ID {
		t.Fatalf("wrong payment links: %+v", cases)
	}
	if len(PaymentTrades(trades)) != 2 {
		t.Fatal("authorization and clearing counted twice")
	}
	if !TradeAt(b.Trade).Equal(autoTime("2026-10-15T21:36:23+08:00")) {
		t.Fatal("issuer UTC timestamp not corrected")
	}
	if !TradeAt(a.Trade).Equal(autoTime("2026-10-15T17:24:02+08:00")) {
		t.Fatal("local timestamp shifted")
	}
}
func TestAutoReconcileFIFORegardlessOfAuthorizationCode(t *testing.T) {
	cases := []Case{autoCase("b", "2026-10-15T12:00:00+08:00"), autoCase("a", "2026-10-15T10:00:00+08:00")}
	trades := []TradeRecord{autoTrade("b-pay", "1", "B", "2026-10-15T11:59:30+08:00"), autoTrade("a-pay", "1", "A", "2026-10-15T09:59:30+08:00"), autoTrade("fifo", "5", "", "2026-10-15T14:00:00+08:00"), autoTrade("auth", "5", "A", "2026-10-15T15:00:00+08:00")}
	now := autoTime("2026-10-15T16:00:00+08:00")
	AutoReconcile(cases, trades, now)
	if trades[2].OrderID != "a" || trades[3].OrderID != "b" || trades[3].Source != "auto_fifo" {
		t.Fatalf("authorization code reordered FIFO: %+v", trades)
	}
	oldCases := append([]Case(nil), cases...)
	oldTrades := append([]TradeRecord(nil), trades...)
	AutoReconcile(cases, trades, now.Add(time.Hour))
	if !reflect.DeepEqual(cases, oldCases) || !reflect.DeepEqual(trades, oldTrades) {
		t.Fatal("repeated reconciliation changed saved results")
	}
	// Without usable codes, both streams are matched oldest first regardless of input order.
	trades[3].Trade.AuthCode = ""
	AutoReconcile(cases, trades, now)
	if trades[2].OrderID != "a" || trades[3].OrderID != "b" {
		t.Fatal("FIFO order incorrect")
	}
}
func TestAutoReconcileExclusionsAndCorrections(t *testing.T) {
	now := autoTime("2026-10-15T16:00:00+08:00")
	for _, kind := range []string{"pending", "closure", "wrong-card", "unknown-currency", "failed-payment-code", "too-early", "mixed-currency"} {
		t.Run(kind, func(t *testing.T) {
			cases := []Case{autoCase("a", "2026-10-15T10:00:00+08:00")}
			p := autoTrade("pay", "1", "A", "2026-10-15T09:59:30+08:00")
			r := autoTrade("refund", "5", "", "2026-10-15T14:00:00+08:00")
			switch kind {
			case "pending":
				r.Trade.Status = "1"
			case "closure":
				r.Trade.Type = "17"
			case "wrong-card":
				r.CardID = "other"
			case "unknown-currency":
				r.Trade.CardCurrency = ""
			case "failed-payment-code":
				p.Trade.Type = "3"
				r.Trade.AuthCode = "A"
			case "too-early":
				r.Trade.TradeTime = "2026-10-15T08:00:00+08:00"
			case "mixed-currency":
				r.Trade.CardCurrency = "USD"
			}
			trades := []TradeRecord{p, r}
			AutoReconcile(cases, trades, now)
			if trades[1].Confirmed || trades[1].OrderID != "" {
				t.Fatalf("invalid return matched: %+v", trades[1])
			}
		})
	}
	cases := []Case{autoCase("a", "2026-10-15T10:00:00+08:00")}
	trades := []TradeRecord{autoTrade("pay", "1", "A", "2026-10-15T09:59:30+08:00"), autoTrade("refund", "5", "A", "2026-10-15T14:00:00+08:00")}
	AutoReconcile(cases, trades, now)
	trades[1].Trade.CardAmount = "5700.23"
	AutoReconcile(cases, trades, now.Add(time.Hour))
	if trades[1].Amount != "5700.23" || !trades[1].Confirmed {
		t.Fatal("supplier amount correction not recalculated")
	}
	trades[1].Trade.Status = "1"
	AutoReconcile(cases, trades, now)
	if trades[1].Confirmed || trades[1].OrderID != "" {
		t.Fatal("pending return remained confirmed")
	}
}

func TestAutoReconcilePreservesPaymentReferencedByManualReturn(t *testing.T) {
	cases := []Case{autoCase("a", "2026-10-15T10:00:00+08:00")}
	auth := autoTrade("pay", "1", "A", "2026-10-15T09:59:30+08:00")
	ret := autoTrade("refund", "5", "A", "2026-10-15T14:00:00+08:00")
	ret.OrderID, ret.OriginalTradeID, ret.Source, ret.Confirmed = "a", auth.ID, "manual_verified", true
	ret.Amount, ret.Currency = "5700", "PHP"
	cases[0].OriginalTradeID, cases[0].ChargeEvidence = auth.ID, "auto: saved payment"
	clearing := autoTrade("clear", "4", "A", "2026-10-16T10:00:00+08:00")
	trades := []TradeRecord{auth, ret, clearing}
	AutoReconcile(cases, trades, autoTime("2026-10-16T16:00:00+08:00"))
	if cases[0].OriginalTradeID != auth.ID || !reflect.DeepEqual(trades[1], ret) {
		t.Fatal("historical manual return evidence changed")
	}
}

func TestAutoReconcileReversalAndPostedRefundWithoutRemoval(t *testing.T) {
	for _, kind := range []string{"2", "5", "16"} {
		t.Run(kind, func(t *testing.T) {
			cases := []Case{autoCase("a", "2026-10-15T21:36:26+08:00")}
			cases[0].RequestedAt = nil
			p := autoTrade("pay", "1", "259562", "2026-10-15 13:36:23")
			p.Trade.Status, p.Trade.CreatedAt = "1", "2026-10-15 21:40:34"
			r := autoTrade("return", kind, "259562", "2026-10-15 15:24:17")
			r.Trade.CardAmount, r.Trade.CreatedAt = "5780.87", "2026-10-15 23:25:22"
			if kind == "2" {
				r.Trade.Status = "1"
			}
			trades := []TradeRecord{r, p}
			AutoReconcile(cases, trades, autoTime("2026-10-15T23:40:00+08:00"))
			if !trades[0].Confirmed || trades[0].OrderID != "a" || trades[0].Amount != "5780.87" {
				t.Fatal(trades[0])
			}
			if kind == "2" && trades[0].Trade.Status != "1" {
				t.Fatal("issuer state overwritten")
			}
		})
	}
}
