package restock

import (
	"testing"
	"time"
)

func TestForecastDisplayCappedVersusGap(t *testing.T) {
	c, s, n := fixture()
	s.Accounts[0].Windows[0].Used = 10
	s.Accounts[0].Windows[0].Rate = .1
	s.Accounts[0].Windows[0].ResetAt = n.Add(5 * time.Hour).Unix()
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.SupplyMinutes == nil || *d.SupplyMinutes != 60 || !d.SupplyCapped {
		t.Fatalf("horizon not labelled: %+v", d)
	}
	expiry := n.Add(60 * time.Minute)
	s.Accounts[0].ExpiresAt = &expiry
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.SupplyMinutes == nil || *d.SupplyMinutes != 60 || d.SupplyCapped {
		t.Fatalf("gap at horizon mislabelled: %+v", d)
	}
	s.Accounts[0].Windows[0].Samples = 0
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.SupplyMinutes != nil || d.SupplyCapped {
		t.Fatal("missing samples reported as supply")
	}
}
func TestObservedRateRetainsZeroAndResets(t *testing.T) {
	n := time.Now()
	p := Window{Seconds: 18000, ResetAt: n.Add(time.Hour).Unix(), Used: 20, Rate: 5, Samples: 3, At: n.Add(-time.Minute)}
	w := p
	w.At = n
	got := ObserveWindows([]Window{w}, []Window{p}, true, 2*time.Minute)[0]
	if got.ObservedRate == nil || *got.ObservedRate != 0 || got.Rate != 3 || got.SampleSeconds != 60 {
		t.Fatalf("raw zero hidden by smoothing: %+v", got)
	}
	w.ResetAt++
	got = ObserveWindows([]Window{w}, []Window{p}, true, 2*time.Minute)[0]
	if got.ObservedRate != nil || got.SampleSeconds != 0 || got.Samples != 0 {
		t.Fatal("reset generated consumption")
	}
}
