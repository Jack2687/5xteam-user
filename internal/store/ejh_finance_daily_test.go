package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestDailyConsumptionReplacesBaselineAndPollingDates(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	const email = "daily@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "daily-order", "success")
	o.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 15, 23, 0, 0, 0, financeZone)
	checked := start.Add(2 * time.Hour)
	for _, value := range []float64{0, 100, 120} {
		_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.CostProvider = "sub2"
			p.CostConnectionIdentity = "server-a"
			p.Sub2AccountID = 42
			p.UserCostDownstreamIdentity = "42"
			p.UserCostDownstreamSnapshot = value
			p.ProPushStartedAt = &start
			p.CostCheckedAt = &checked
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := EJHRecords[EJHConsumption](s, "finance_usage")
	original := rows[0]
	if original.StartDay != "2026-10-15" || original.EndDay != "2026-10-16" || original.Baseline != 100 {
		t.Fatal(original)
	}
	amounts := map[string]float64{"2026-10-15": 90, "2026-10-16": 32} // final requests after last cost poll
	if err := s.SaveEJHDailyConsumption(original, amounts, checked.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEJHDailyConsumption(original, amounts, checked.Add(2*time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	rows, _ = EJHRecords[EJHConsumption](s, "finance_usage")
	days, _ := EJHRecords[EJHConsumptionDay](s, "finance_usage_day")
	if len(days) != 2 || rows[0].Total != 122 || rows[0].Baseline != 0 || rows[0].DailyCheckedAt == nil {
		t.Fatal(rows, days)
	}
	for _, d := range days {
		if d.Source != "sub2_daily" || d.Amount != amounts[d.Day] {
			t.Fatal(d)
		}
	}
	for _, bad := range []map[string]float64{{"2026-10-15": 90}, {"2026-10-15": 0, "2026-10-16": 0}} {
		if err := s.SaveEJHDailyConsumption(rows[0], bad, checked, ""); err == nil {
			t.Fatal("partial or purged history accepted")
		}
	}
	if err := s.SaveEJHDailyConsumption(rows[0], nil, checked, "offline"); err != nil {
		t.Fatal(err)
	}
	rows, _ = EJHRecords[EJHConsumption](s, "finance_usage")
	if rows[0].Total != 122 || rows[0].DailyError != "offline" {
		t.Fatal(rows)
	}
	newChecked := checked.Add(10 * time.Minute)
	_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.UserCostDownstreamSnapshot = 130; p.CostCheckedAt = &newChecked })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveEJHDailyConsumption(original, amounts, checked, ""); err == nil {
		t.Fatal("stale response overwrote newer binding sample")
	}
	days, _ = EJHRecords[EJHConsumptionDay](s, "finance_usage_day")
	if len(days) != 2 {
		t.Fatal("polling date was appended after daily migration", days)
	}
	// Deleting credentials leaves all daily amounts and the lookup binding intact.
	if _, err = s.db.Exec("DELETE FROM mail_accounts WHERE email=?", email); err != nil {
		t.Fatal(err)
	}
	rows, _ = EJHRecords[EJHConsumption](s, "finance_usage")
	if rows[0].DownstreamID != 42 || rows[0].ConnectionIdentity != "server-a" || rows[0].Total != 130 {
		t.Fatal(rows)
	}
}
