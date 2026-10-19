package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
)

func TestEJHFinanceConsumptionAndSnapshots(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	const email = "finance@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "finance-order", "success")
	o.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{100, 112, 112, 80, 120} {
		if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.UserCostDownstreamIdentity = "finance-42"
			p.UserCostDownstreamSnapshot = value
		}); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := EJHRecords[EJHConsumption](s, "finance_usage")
	if err != nil || len(usage) != 1 || usage[0].Total != 120 || usage[0].Baseline != 100 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	days, err := EJHRecords[EJHConsumptionDay](s, "finance_usage_day")
	if err != nil || len(days) != 1 || days[0].Amount != 20 {
		t.Fatalf("duplicate/reset inflated usage: %+v %v", days, err)
	}
	var remote ejh.CardRecord
	if err = s.EJHGet("card", card.ID, &remote); err != nil {
		t.Fatal(err)
	}
	remote.Remote.CVV = "secret-cvv"
	if err = s.EJHPut("card", card.ID, remote); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, financeZone)
	for _, at := range []time.Time{now, now.Add(time.Minute), now.Add(24 * time.Hour)} {
		if err = s.CaptureEJHFinance(at); err != nil {
			t.Fatal(err)
		}
	}
	snapshots, err := EJHRecords[EJHFinancialSnapshot](s, "finance_snapshot")
	if err != nil || len(snapshots) != 4 {
		t.Fatalf("snapshots: %d %v", len(snapshots), err)
	}
	raw, _ := json.Marshal(snapshots)
	if strings.Contains(string(raw), "secret-cvv") || strings.Contains(string(raw), remote.Remote.Number) {
		t.Fatal("financial snapshots contain card secrets")
	}
	if _, err = s.db.Exec(`DELETE FROM mail_accounts WHERE email=?`, email); err != nil {
		t.Fatal(err)
	}
	usage, err = EJHRecords[EJHConsumption](s, "finance_usage")
	if err != nil || len(usage) != 1 || usage[0].Total != 120 {
		t.Fatal("credential deletion erased usage", usage, err)
	}
}

func TestEJHFinanceRechargeHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, amount := range []string{"2000", "2000", "3000", "2900"} {
		if _, err = s.SaveEJHRechargeLedger(amount); err != nil {
			t.Fatal(err)
		}
	}
	events, err := EJHRecords[EJHRechargeEvent](s, "finance_recharge")
	if err != nil || len(events) != 3 {
		t.Fatalf("events: %+v %v", events, err)
	}
	if events[0].Delta != "-100.00" || events[1].Delta != "1000.00" || !events[2].Baseline {
		t.Fatal(events)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err = EJHRecords[EJHRechargeEvent](s, "finance_recharge")
	if err != nil || len(events) != 3 {
		t.Fatal("restart lost history", err)
	}
}
