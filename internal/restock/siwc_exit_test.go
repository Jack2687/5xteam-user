package restock

import (
	"testing"
	"time"
)

func TestSIWCExitForecastUsesFiveHourPolicyAndOriginalDeadline(t *testing.T) {
	c, _, now := exitFixture()
	threshold := 90.
	a := Account{ID: -42, SIWC: true, Ready: true, Capacity: 50, Used: 40, Quota5HThreshold: &threshold, Windows: []Window{{Seconds: 18000, ResetAt: now.Add(time.Hour).Unix(), At: now, Used: 86, Rate: 2, Samples: 3}}}
	r := exitRow(c, a, now)
	if r.ExitMinutes == nil || *r.ExitMinutes != 2 {
		t.Fatalf("SIWC 5h policy not forecast: %+v", r)
	}
	deadline := now.Add(time.Minute)
	a.ExitDeadline = &deadline
	if r = exitRow(c, a, now); r.ExitMinutes == nil || *r.ExitMinutes != 1 {
		t.Fatal("original deadline ignored", r)
	}
	a.ExitDeadline = nil
	a.Windows[0].ResetAt = now.Add(time.Minute).Unix()
	if r = exitRow(c, a, now); r.Risk {
		t.Fatal("reset before predicted exit must not replace", r)
	}
}
