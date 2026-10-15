package restock

import "testing"

func TestForecastHorizonDoesNotSuppressOtherRules(t *testing.T) {
	for _, timeEnabled := range []bool{false, true} {
		c, s, now := fixture()
		c.TimeEnabled = timeEnabled
		s.Accounts[0].Windows = nil
		c.ForecastMinutes = c.TargetMinutes
		d := Evaluate(c, s, nil, Limits{}, c.Confirmations, 20, 20, now)
		if d.ConcurrencyNeed != 1 || d.Suggested != 1 || d.TimeNeed != 0 {
			t.Fatalf("time enabled %v: horizon suppressed independent pressure: %+v", timeEnabled, d)
		}
		s.Accounts = nil
		c.Reserve = 1
		d = Evaluate(c, s, nil, Limits{}, c.Confirmations, 20, 20, now)
		if d.ReserveNeed != 1 || d.Suggested != 1 {
			t.Fatalf("reserve suppressed: %+v", d)
		}
	}
}
