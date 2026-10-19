package store

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"testing"
)

func TestTokenseekTransferPreservesCountryAndPoints(t *testing.T) {
	src, dst := transferStore(t), transferStore(t)
	email := "points-transfer@example.com"
	transferSource(t, src, email)
	o := transferOrder(email, "points-transfer", "processing")
	o.Order.Provider, o.Order.PlanCode, o.Order.Country, o.Order.CreditQuantity = gptpay.Tokenseek, "codex_point", "JP", 1250
	o.Snapshot.Provider, o.Snapshot.Country = gptpay.Tokenseek, "JP"
	o.Snapshot.Input.PlanCode, o.Snapshot.Input.Country, o.Snapshot.Input.CreditQuantity = "codex_point", "JP", 1250
	if err := src.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	file, err := src.ExportProAccounts([]string{email}, model.ProDownstreamRef{})
	if err != nil {
		t.Fatal(err)
	}
	file = transferWire(t, file)
	if err = ValidateProTransfer(file); err != nil {
		t.Fatal(err)
	}
	if _, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, false, model.ProDownstreamRef{}); err != nil {
		t.Fatal(err)
	}
	saved, snapshot, err := dst.GPTPayOrder(o.Order.ID)
	if err != nil || saved.Country != "JP" || saved.CreditQuantity != 1250 || snapshot.Input.Country != "JP" || snapshot.Input.CreditQuantity != 1250 {
		t.Fatal("purchase snapshot lost", err)
	}
	file.Accounts[0].Orders[0].Order.CreditQuantity = 250
	if err = ValidateProTransfer(file); err == nil {
		t.Fatal("inconsistent point quantity accepted")
	}
	file.Accounts[0].Orders[0].Order.CreditQuantity = 1250
	file.Accounts[0].Orders[0].Order.Country = "PH"
	if err = ValidateProTransfer(file); err == nil {
		t.Fatal("inconsistent country accepted")
	}
}
