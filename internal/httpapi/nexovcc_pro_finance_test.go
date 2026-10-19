package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNexovccProFinancialDetailsAndCanonicalCard(t *testing.T) {
	s, _ := nexFixtureServer(t)
	card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "finance-card", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC()
	order := gptpay.Order{ID: "pro-finance", Email: "finance@example.com", CardID: "old-alias", CardName: "activation snapshot", CardLast4: "4242", Status: "success", PlanCode: "pro5", CreatedAt: at, SucceededAt: &at}
	order.Remote.PaymentCurrency = "PHP"
	order.Remote.PaymentAmount = "5794.64"
	snapshot := gptpay.Snapshot{APIKey: "secret-test-key", Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}}}
	if err = s.store.CreateGPTPayOrder(order, snapshot); err != nil {
		t.Fatal(err)
	}
	finance := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{
		{ID: "paid", Serial: card.Serial, Authorization: "auth", Currency: "USD", Amount: "92.33", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "PENDING", At: at.Add(time.Minute)},
		{ID: "returned", Serial: card.Serial, Authorization: "auth", Currency: "USD", Amount: "92.28", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "REVERSED", At: at.Add(10 * time.Minute)},
	}, Funds: []nexovcc.Fund{{ID: "fee", Serial: card.Serial, Link: "returned", Currency: "USD", Amount: "-0.50", Kind: 2003, At: at.Add(10 * time.Minute)}}}
	if err = s.store.SaveNexovccFinance(finance); err != nil {
		t.Fatal(err)
	}
	check := func(wantState string) {
		t.Helper()
		rows, err := s.proAccountListWithCards([]model.MailAccountProfile{{Email: order.Email}})
		if err != nil || len(rows) != 1 || len(rows[0].FinancialCases) != 1 {
			t.Fatal(rows, err)
		}
		row := rows[0]
		c := row.FinancialCases[0]
		if row.ActivationCard == nil || row.ActivationCard.NexovccCardID != card.ID || row.ActivationCard.EJHCardID != "" {
			t.Fatal("wrong issuer navigation", row.ActivationCard)
		}
		if c.State != wantState || c.Nexovcc == nil || c.Nexovcc.CardID != card.ID || c.Nexovcc.Match == nil {
			t.Fatal("missing Nexovcc details", c)
		}
		g := c.Nexovcc.Match.Group
		if g.Payment != "92.33" || g.Returned != "92.28" || g.Cost != "0.55" {
			t.Fatal("wrong financial snapshot", g)
		}
		w := httptest.NewRecorder()
		s.listGPTPayCardCases(w, httptest.NewRequest("GET", "/api/gptpay/card-cases", nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Data []proFinancialCase `json:"data"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data) != 1 || response.Data[0].Nexovcc == nil || response.Data[0].State != wantState {
			t.Fatal("card list missed financial data", w.Body.String(), err)
		}
		encoded, _ := json.Marshal(row)
		for _, secret := range []string{snapshot.APIKey, snapshot.Input.Number} {
			if strings.Contains(string(encoded)+w.Body.String(), secret) {
				t.Fatal("exposed payment secrets")
			}
		}
	}
	check("returned")
	if err = s.store.SetNexovccCase(order.ID, "waiting", "Verified refund still pending", 0); err != nil {
		t.Fatal(err)
	}
	check("waiting")
}
