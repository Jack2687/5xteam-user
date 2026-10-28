package ejh

import "testing"

func TestAutoCancelReturnsRequireCompleteRealEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*CardRecord, *[]Case, *[]TradeRecord)
		allow  bool
	}{
		{"full_refund", nil, true},
		{"posted_refund_type_16", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.Type = "16" }, true},
		{"refund_net_settlement_fee", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) {
			(*t)[0].Trade.Type = "4"
			(*t)[1].Trade.BillAmount = "18.8"
		}, true},
		{"unposted_refund_type_16", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) {
			(*t)[1].Trade.Type, (*t)[1].Trade.Status = "16", "1"
		}, false},
		{"reversal_releases_hold", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) {
			(*t)[1].Trade.Type = "2"
			(*t)[1].Trade.Status = "1"
			(*t)[1].Trade.BillAmount = ""
		}, true},
		{"fx_same_transaction_principal", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) {
			(*t)[1].Trade.CardAmount = "99"
			(*t)[1].Amount = "99"
		}, true},
		{"no_returns", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { *t = (*t)[:1] }, false},
		{"partial_principal", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.TradeAmount = "10" }, false},
		{"currency_mismatch", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.TradeCurrency = "EUR" }, false},
		{"unsettled_refund", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.Status = "1" }, false},
		{"missing_settlement", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.BillAmount = "" }, false},
		{"manual_amount_only", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.CardAmount = "0" }, false},
		{"incorrect_confirmation", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Amount = "90" }, false},
		{"missing_original", func(_ *CardRecord, c *[]Case, _ *[]TradeRecord) { (*c)[0].OriginalTradeID = "missing" }, false},
		{"unconfirmed", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Confirmed = false }, false},
		{"wrong_card", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { (*t)[1].Trade.OrderNo = "other" }, false},
		{"waiting_override", func(_ *CardRecord, c *[]Case, _ *[]TradeRecord) { (*c)[0].State = "waiting" }, false},
		{"additional_payment", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) {
			v := (*t)[0]
			v.ID = "other"
			v.Trade.TradeNo = "other"
			*t = append(*t, v)
		}, false},
		{"another_waiting_account", func(_ *CardRecord, c *[]Case, _ *[]TradeRecord) {
			*c = append(*c, Case{ID: "second", CardID: "card", PaymentStatus: "success", State: "waiting"})
		}, false},
		{"duplicate_returns", func(_ *CardRecord, _ *[]Case, t *[]TradeRecord) { *t = append(*t, (*t)[1]) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := CardRecord{ID: "card", Remote: Card{OrderNo: "issuer"}}
			cases := []Case{{ID: "order", CardID: "card", PaymentStatus: "success", State: "returned", OriginalTradeID: "pay"}}
			trades := []TradeRecord{
				{ID: "pay", CardID: "card", Trade: Trade{OrderNo: "issuer", TradeNo: "pay", Type: "1", Status: "2", CardAmount: "100", CardCurrency: "PHP", TradeAmount: "20", TradeCurrency: "USD"}},
				{ID: "return", CardID: "card", OrderID: "order", OriginalTradeID: "pay", Confirmed: true, Amount: "100", Currency: "PHP", Trade: Trade{OrderNo: "issuer", TradeNo: "return", Type: "5", Status: "2", CardAmount: "100", CardCurrency: "PHP", TradeAmount: "20", TradeCurrency: "USD", BillAmount: "19", BillCurrency: "USD"}},
			}
			if tc.change != nil {
				tc.change(&card, &cases, &trades)
			}
			if err := ValidateAutoCancelReturns(card, cases, trades); (err == nil) != tc.allow {
				t.Fatalf("allow=%v err=%v", tc.allow, err)
			}
		})
	}
}

func TestAutoCancelOnlyNormalActiveCards(t *testing.T) {
	for _, status := range []string{"", "0", "1", "2", "3", "4", "5", "6", "7", "8", "unknown"} {
		for _, pending := range []bool{false, true} {
			c := CardRecord{Platform: "oai", NormalPending: pending, Remote: Card{Status: status}}
			want := !pending && (status == "1" || status == "3")
			if AutoCancelNormalCard(c) != want {
				t.Fatalf("status=%s pending=%v", status, pending)
			}
			c.Remote.Locked = "1"
			if AutoCancelNormalCard(c) {
				t.Fatal("locked card allowed")
			}
		}
	}
	if AutoCancelNormalCard(CardRecord{Platform: "cc", Remote: Card{Status: "1"}}) {
		t.Fatal("CC card allowed")
	}
}

func TestAutoCancelSettingsDefaultsAndBounds(t *testing.T) {
	v := DefaultSettings()
	if v.AutoCancelEnabled || v.AutoCancelIntervalSeconds != 300 {
		t.Fatal(v)
	}
	for _, seconds := range []int{-1, 0, 59, 60, 300, 86400, 86401} {
		v.AutoCancelIntervalSeconds = seconds
		got, err := Normalize(v)
		want := seconds == 0 || seconds >= 60 && seconds <= 86400
		if (err == nil) != want {
			t.Fatalf("seconds=%d err=%v", seconds, err)
		}
		if seconds == 0 && got.AutoCancelIntervalSeconds != 300 {
			t.Fatal("missing default")
		}
	}
}

func TestAutoCancelAuthorizationCancellationNetCredit(t *testing.T) {
	for _, amount := range []string{"8900.03", "8898.24", "8901.17", "8906.17", "8904.08", "8901.83"} {
		for _, scenario := range []string{"matched_reversal", "wrong_authorization", "missing_authorization", "captured", "ordinary_refund", "unconfirmed", "not_received", "positive_partial_principal"} {
			t.Run(amount+"/"+scenario, func(t *testing.T) {
				card := CardRecord{ID: "card", Remote: Card{OrderNo: "issuer"}}
				cases := []Case{{ID: "order", CardID: "card", PaymentStatus: "success", State: "returned", OriginalTradeID: "pay"}}
				p := TradeRecord{ID: "pay", CardID: "card", Trade: Trade{OrderNo: "issuer", TradeNo: "pay", Type: "1", Status: "1", AuthCode: "auth", CardAmount: "8919.64", CardCurrency: "PHP", TradeAmount: "0", BillAmount: "0"}}
				r := TradeRecord{ID: "return", CardID: "card", OrderID: "order", OriginalTradeID: "pay", Confirmed: true, Amount: amount, Currency: "PHP", Trade: Trade{OrderNo: "issuer", TradeNo: "return", Type: "2", Status: "1", AuthCode: "auth", CardAmount: amount, CardCurrency: "PHP", TradeAmount: "0.00", BillAmount: "0.00"}}
				switch scenario {
				case "wrong_authorization":
					r.Trade.AuthCode = "other"
				case "missing_authorization":
					p.Trade.AuthCode = ""
					r.Trade.AuthCode = ""
				case "captured":
					p.Trade.Type = "4"
					p.Trade.Status = "2"
				case "ordinary_refund":
					r.Trade.Type = "5"
					r.Trade.Status = "2"
					r.Trade.BillAmount = "140"
					r.Trade.BillCurrency = "USD"
				case "unconfirmed":
					r.Confirmed = false
				case "not_received":
					r.Trade.Status = "0"
				case "positive_partial_principal":
					p.Trade.TradeAmount = "150"
					p.Trade.TradeCurrency = "USD"
					r.Trade.TradeAmount = "140"
					r.Trade.TradeCurrency = "USD"
				}
				err := ValidateAutoCancelReturns(card, cases, []TradeRecord{p, r})
				if (err == nil) != (scenario == "matched_reversal") {
					t.Fatalf("unexpected eligibility: %v", err)
				}
			})
		}
	}
}
