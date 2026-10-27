package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

func TestNexovccRefundDescriptorsAcrossDashboardAndPro(t *testing.T) {
	checkNexovccRefundDashboardAndPro(t, false)
}

func TestNexovccSettlementCurrencyRefundAcrossDashboardAndPro(t *testing.T) {
	checkNexovccRefundDashboardAndPro(t, true)
}

func checkNexovccRefundDashboardAndPro(t *testing.T, settledUSD bool) {
	t.Helper()
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("cached refund accounting must not request the issuer")
		return nexovcc.Response{}, nil
	}
	card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "fixture-refund-card", Status: "1", Balance: "169.39"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	local, secret, err := s.store.GPTPayCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	limit := 2
	local.MaxAccounts = &limit
	if _, err = s.store.SaveGPTPayCard(local, secret); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../nexovcc/testdata/refund-descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f nexovcc.Finance
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{6, 3} {
		trade := f.Trades[index]
		order := gptpay.Order{ID: trade.Authorization, Email: trade.Authorization + "@example.com", CardID: card.ID, Status: "success", PlanCode: "pro20", CreatedAt: trade.At, SucceededAt: &trade.At}
		order.Remote.PaymentCurrency, order.Remote.PaymentAmount = "PHP", "8919.64"
		if err = s.store.CreateGPTPayOrder(order, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}}}); err != nil {
			t.Fatal(err)
		}
		if err = s.store.SaveNexovccFinance(f); err != nil {
			t.Fatal(err)
		}
	}
	// Existing PHP payment associations must survive the issuer changing its
	// settlement/refund representation to USD on the next complete snapshot.
	if settledUSD {
		for _, i := range []int{0, 2} {
			f.Trades[i].MerchantCurrency = "USD"
			f.Trades[i].MerchantAmount = f.Trades[i].Amount
		}
	}
	if err = s.store.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.nexovccDashboard(w, httptest.NewRequest("GET", "/api/nexovcc/dashboard", nil))
	var result struct {
		Data struct {
			Cases  []store.NexovccCase  `json:"cases"`
			Groups []nexovcc.TradeGroup `json:"groups"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if len(result.Data.Cases) != 2 {
		t.Fatal(result.Data.Cases)
	}
	for _, c := range result.Data.Cases {
		if c.State != "returned" || c.Match == nil || !c.Match.Group.FullyReturned {
			t.Fatal(c)
		}
		want := "6.44"
		if c.ID == "payment" {
			want = "7.75"
		}
		if c.Match.Group.Cost != want {
			t.Fatal(c.Match.Group)
		}
	}
	if !nexovccRecycleCandidate(card, result.Data.Cases, result.Data.Groups) {
		t.Fatal("final related charge must not be treated as another unreturned sale")
	}
	pro, err := s.proAccountListWithCards([]model.MailAccountProfile{{Email: "payment@example.com"}})
	if err != nil || len(pro) != 1 || len(pro[0].FinancialCases) != 1 {
		t.Fatal(pro, err)
	}
	c := pro[0].FinancialCases[0]
	if c.State != "returned" || c.Nexovcc == nil || c.Nexovcc.Match.Group.Returned != "141.23" {
		t.Fatal(c)
	}
}
