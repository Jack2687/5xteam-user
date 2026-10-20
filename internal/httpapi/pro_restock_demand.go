package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/sub2"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func demandServerKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

func (s *Server) sampleRestockDemand(ctx context.Context, c restock.Config, p model.ProSettings, snap restock.Snapshot) restock.DemandGate {
	now := time.Now()
	g := restock.DemandGate{MainAt: snap.At, ValidUntil: snap.At.Add(time.Duration(c.FreshSeconds) * time.Second)}
	if !restockSnapshotFresh(c, snap, now) {
		g.Reason = "禁补中：主监控并发数据待确认"
		return g
	}
	primary := map[int64]int{}
	for _, a := range snap.Accounts {
		if a.Used < 0 {
			g.Reason = "禁补中：主监控并发无效"
			return g
		}
		primary[a.ID] = max(primary[a.ID], a.Used)
	}
	for _, used := range primary {
		g.MonitorUsed += used
	}
	g.TotalUsed = g.MonitorUsed
	f, targetID, err := s.store.ProFallbackConfig()
	if err != nil {
		g.Reason = "禁补中：兜底配置读取失败"
		return g
	}
	g.FallbackAccounts = len(f.AccountIDs)
	if len(f.AccountIDs) == 0 {
		g.Valid = true
		return g
	}
	if err = f.Validate(); err != nil || targetID == "" || len(f.SelectedGroupIDs()) == 0 {
		g.Reason = "禁补中：已勾选兜底账号的连接或分组配置无效"
		return g
	}
	target, err := s.store.DownstreamTarget(targetID)
	if err != nil {
		g.Reason = "禁补中：兜底连接读取失败"
		return g
	}
	client := s.fallbackClient(targetID)
	selected := map[int64]bool{}
	for _, id := range f.AccountIDs {
		selected[id] = true
	}
	observed := map[int64]int{}
	for _, group := range f.SelectedGroupIDs() {
		loads, err := client.MonitorConcurrency(ctx, target.Sub2, target.Secret, group)
		if err != nil {
			g.Reason = "禁补中：" + fallbackError("兜底并发采集", err)
			return g
		}
		if !validDemandLoads(loads, time.Now(), c.FreshSeconds) {
			g.Reason = "禁补中：兜底并发数据过期或采集不完整"
			return g
		}
		if g.FallbackAt.IsZero() || loads.At.Before(g.FallbackAt) {
			g.FallbackAt = loads.At
		}
		for id, load := range loads.Account {
			if !selected[id] {
				continue
			}
			if load.AccountID != id || load.Used < 0 || (load.Known != nil && !*load.Known) {
				g.Reason = "禁补中：已勾选兜底账号的并发数据无效"
				return g
			}
			observed[id] = max(observed[id], load.Used)
		}
	}
	if len(observed) != len(selected) {
		g.Reason = fmt.Sprintf("禁补中：兜底账号并发采集不完整（%d/%d），等待重新检测", len(observed), len(selected))
		return g
	}
	sameServer := demandServerKey(p.Sub2.URL) == demandServerKey(target.Sub2.URL)
	for id, used := range observed {
		if sameServer {
			used = max(0, used-primary[id])
		}
		g.FallbackUsed += used
	}
	g.TotalUsed += g.FallbackUsed
	if expiry := g.FallbackAt.Add(time.Duration(c.FreshSeconds) * time.Second); expiry.Before(g.ValidUntil) {
		g.ValidUntil = expiry
	}
	g.Valid = true
	return g
}

func validDemandLoads(loads sub2.MonitorLoads, now time.Time, seconds int) bool {
	if !loads.Enabled || loads.Account == nil || loads.At.IsZero() || now.Sub(loads.At) > time.Duration(seconds)*time.Second || loads.At.After(now.Add(5*time.Second)) {
		return false
	}
	q := loads.Quality
	return q == nil || (q.Complete && q.Failed == 0 && q.Expected == len(loads.Account) && q.Succeeded == q.Expected)
}
