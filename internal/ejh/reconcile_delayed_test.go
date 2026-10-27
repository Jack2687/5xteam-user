package ejh

import (
	"reflect"
	"testing"
	"time"
)

func delayedReturnFixture() ([]Case, []TradeRecord) {
	cases := []Case{autoCase("first", "2026-10-27T21:38:38+08:00"), autoCase("second", "2026-10-27T23:26:30+08:00")}
	trades := []TradeRecord{
		autoTrade("first-pay", "1", "A", "2026-10-27 13:38:33"),
		autoTrade("second-pay", "1", "B", "2026-10-27 15:26:24"),
		autoTrade("first-return", "2", "A", "2026-10-27 14:59:39"),
		autoTrade("second-return", "2", "B", "2026-10-27 17:15:21"),
	}
	created := []string{"2026-10-27 21:40:06", "2026-10-27 23:39:56", "2026-10-27 23:10:00", "2026-10-28 02:12:49"}
	amounts := []string{"8919.64", "8919.64", "8903.80", "8898.18"}
	for i := range trades {
		trades[i].Trade.Status, trades[i].Trade.CreatedAt, trades[i].Trade.CardAmount = "1", created[i], amounts[i]
	}
	return cases, trades
}

func TestAutoReconcileDelayedUTCReversal(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		cases, trades := delayedReturnFixture()
		if reverse {
			for i, j := 0, len(trades)-1; i < j; i, j = i+1, j-1 {
				trades[i], trades[j] = trades[j], trades[i]
			}
		}
		raw := map[string]Trade{}
		for _, tr := range trades {
			raw[tr.ID] = tr.Trade
		}
		now := autoTime("2026-10-28T03:16:54+08:00")
		AutoReconcile(cases, trades, now)
		for _, tr := range trades {
			if !reflect.DeepEqual(raw[tr.ID], tr.Trade) {
				t.Fatal("raw issuer evidence changed")
			}
			if tr.Trade.Type == "2" && (!tr.Confirmed || tr.OrderID+"-return" != tr.ID) {
				t.Fatalf("wrong return: %+v", tr)
			}
		}
		before := append([]TradeRecord(nil), trades...)
		AutoReconcile(cases, trades, now.Add(time.Hour))
		if !reflect.DeepEqual(before, trades) {
			t.Fatal("repeat reconciliation changed attribution")
		}
	}
}

func TestAutoReconcileDelayedReturnNeedsAuthorizationEvidence(t *testing.T) {
	for _, scenario := range []string{"missing-auth", "wrong-card", "wrong-currency", "wrong-merchant", "wrong-issuer-card", "local-authorization", "duplicate-authorization", "explicit-timezone", "before-authorization", "after-recording", "pending-refund", "local-return"} {
		t.Run(scenario, func(t *testing.T) {
			_, trades := delayedReturnFixture()
			p, r := &trades[1].Trade, &trades[3].Trade
			switch scenario {
			case "missing-auth":
				r.AuthCode = ""
			case "wrong-card":
				trades[3].CardID = "other"
			case "wrong-currency":
				r.CardCurrency = "USD"
			case "wrong-merchant":
				r.Merchant = "OTHER"
			case "wrong-issuer-card":
				r.OrderNo = "other"
			case "local-authorization":
				p.TradeTime = "2026-10-27 23:26:24"
			case "duplicate-authorization":
				duplicate := trades[1]
				duplicate.ID = "duplicate"
				trades = append(trades, duplicate)
			case "explicit-timezone":
				r.TradeTime = "2026-10-27T17:15:21+08:00"
			case "before-authorization":
				r.TradeTime = "2026-10-27 15:00:00"
			case "after-recording":
				r.CreatedAt = "2026-10-27 23:00:00"
			case "pending-refund":
				r.Type, r.Status = "5", "1"
			case "local-return":
				r.TradeTime = "2026-10-28 01:15:21"
			}
			if got := returnTradeTimes(trades)["second-return"]; !got.Equal(TradeAt(trades[3].Trade)) {
				t.Fatalf("unsupported timezone correction: %s", got)
			}
		})
	}
}
