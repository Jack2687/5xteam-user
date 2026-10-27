package restock

import "fmt"

type CapacityTier struct {
	MinCapacity    int `json:"min_capacity"`
	TriggerPercent int `json:"trigger_percent"`
	TargetPercent  int `json:"target_percent"`
}

// Use the largest matching lower bound. A future exit evaluates its own
// reduced capacity, while in-flight supply never selects today's threshold.
func (c Config) CapacityThresholds(reported int) (trigger, target int) {
	trigger, target = c.TriggerPercent, c.TargetPercent
	if !c.CapacityTiersEnabled {
		return
	}
	capacity, selected := c.EffectiveCapacity(reported), -1
	for _, tier := range c.CapacityTiers {
		if tier.MinCapacity > selected && float64(tier.MinCapacity) <= capacity {
			selected, trigger, target = tier.MinCapacity, tier.TriggerPercent, tier.TargetPercent
		}
	}
	return
}

func (c Config) validateCapacityTiers() error {
	if len(c.CapacityTiers) > 30 {
		return fmt.Errorf("并发容量分档最多 30 档")
	}
	seen := map[int]bool{}
	for _, t := range c.CapacityTiers {
		if t.MinCapacity < 0 || t.MinCapacity > 10000000 || seen[t.MinCapacity] || t.TriggerPercent < 1 || t.TriggerPercent > 100 || t.TargetPercent < 1 || t.TargetPercent >= t.TriggerPercent {
			return fmt.Errorf("分档下限须唯一且非负，触发比例 1～100%%，补后目标须低于该档触发比例")
		}
		seen[t.MinCapacity] = true
	}
	if c.CapacityTiersEnabled && !seen[0] {
		return fmt.Errorf("启用分档时须包含从 0 开始的一档")
	}
	return nil
}
