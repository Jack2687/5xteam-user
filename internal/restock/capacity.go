package restock

const DefaultCapacityPercent = 60

// EffectiveCapacity is the share of Sub2's configured request capacity that
// replenishment may rely on. Keep snapshots and task capacities unscaled so
// changing the percentage never scales an already-scaled observation again.
func (c Config) capacityPercent() int {
	percent := c.CapacityPercent
	if percent < 1 || percent > 100 {
		percent = DefaultCapacityPercent
	}
	return percent
}

func (c Config) EffectiveCapacity(reported int) float64 {
	return float64(max(0, reported)) * float64(c.capacityPercent()) / 100
}

// Trigger occupancy is an integer rounded down, e.g. 80 * 60% * 70% = 33.
// Split before multiplying to retain exact integer rounding without overflow.
func (c Config) ConcurrencyTrigger(reported int) int {
	reported = max(0, reported)
	trigger, _ := c.CapacityThresholds(reported)
	percent := c.capacityPercent() * min(100, max(1, trigger))
	return (reported/10000)*percent + (reported%10000)*percent/10000
}
