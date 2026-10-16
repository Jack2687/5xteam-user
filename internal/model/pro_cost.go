package model

// ClearProCostSnapshot prevents consumption from following a different binding.
func (p *MailAccountProfile) ClearProCostSnapshot() {
	p.CostProvider, p.CostConnectionIdentity, p.CostDownstreamIdentity = "", "", ""
	p.CostDownstreamSnapshot, p.UserCostDownstreamSnapshot = 0, 0
	p.UserCostDownstreamIdentity, p.CostCheckedAt = "", nil
}
