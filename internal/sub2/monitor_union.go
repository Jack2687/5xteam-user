package sub2

import (
	"context"
	"errors"
	"slices"

	"chatgpt-space-merge/internal/model"
)

// Sub2 reports global per-account counters even in a group query. Query each
// group's complete membership, validate it, then count each ID exactly once.
// Consecutive queries can differ in load: use the larger observation, not sum.
func (c *Client) MonitorGroups(ctx context.Context, v model.Sub2Settings, password string, groups []int64) ([]MonitorAccount, MonitorLoads, error) {
	accounts := map[int64]MonitorAccount{}
	out := MonitorLoads{Enabled: true, Account: map[int64]MonitorLoad{}}
	for _, group := range uniquePositiveIDs(groups) {
		rows, e := c.MonitorAccounts(ctx, v, password, group)
		if e != nil {
			return nil, out, e
		}
		load, e := c.MonitorConcurrency(ctx, v, password, group)
		if e != nil {
			return nil, out, e
		}
		if len(rows) != len(load.Account) {
			return nil, out, errors.New("Sub2 分组账号与负载不完整")
		}
		if q := load.Quality; q != nil && (!q.Complete || q.Expected != len(rows) || q.Succeeded != len(rows) || q.Failed != 0) {
			return nil, out, errors.New("Sub2 报告分组采集不完整")
		}
		if out.At.IsZero() || load.At.Before(out.At) {
			out.At = load.At
		}
		for _, a := range rows {
			l, ok := load.Account[a.ID]
			if !ok || l.AccountID != a.ID || l.Capacity != a.Concurrency || l.Used < 0 || l.Waiting < 0 || (l.Known != nil && !*l.Known) {
				return nil, out, errors.New("Sub2 分组成员或容量不一致")
			}
			if previous, exists := accounts[a.ID]; exists {
				if previous.Concurrency != a.Concurrency || previous.Status != a.Status || previous.Schedulable != a.Schedulable || !equalGroupIDs(previous.GroupIDs, a.GroupIDs) {
					return nil, out, errors.New("Sub2 跨分组采集期间账号状态变化，等待重新采集")
				}
				old := out.Account[a.ID]
				l.Used = max(old.Used, l.Used)
				l.Waiting = max(old.Waiting, l.Waiting)
			}
			accounts[a.ID] = a
			out.Account[a.ID] = l
		}
	}
	ids := []int64{}
	for id := range accounts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	rows := []MonitorAccount{}
	for _, id := range ids {
		rows = append(rows, accounts[id])
	}
	return rows, out, nil
}
