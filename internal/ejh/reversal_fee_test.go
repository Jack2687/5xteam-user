package ejh

import (
	"math"
	"testing"
)

func TestReversalFeePercentValidation(t *testing.T) {
	for _, rate := range []float64{0, 0.01, 6, 6.5, 99.99, 100} {
		cfg := DefaultSettings()
		cfg.ReversalFeePercent = rate
		if _, err := Normalize(cfg); err != nil {
			t.Fatalf("valid rate %v: %v", rate, err)
		}
	}
	for _, rate := range []float64{-1, 100.01, 6.001, math.NaN(), math.Inf(1)} {
		cfg := DefaultSettings()
		cfg.ReversalFeePercent = rate
		if _, err := Normalize(cfg); err == nil {
			t.Fatalf("invalid rate accepted: %v", rate)
		}
	}
}
