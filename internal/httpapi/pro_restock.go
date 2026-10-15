package httpapi

import (
	"chapt-space-user/internal/model"
	"chapt-space-user/internal/restock"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type proRestockContextKey struct{}
type proRestockRuntime struct {
	tasks        []restock.Task
	tick         sync.Mutex
	mu           sync.Mutex
	next         time.Time
	quotaNext    time.Time
	quotaBusy    bool
	quota        map[int64][]restock.Window
	quotaPlans   map[int64]string
	quotaBinding string
	lastSnapshot restock.Snapshot
	decision     restock.Decision
	confirmation int
	lastVersion  int
	lastSample   time.Time
	lastRecord   time.Time
	lastReason   string
	monitor      func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error)
}

func restockBinding(p model.ProSettings) string {
	b, _ := json.Marshal(struct {
		Ref    model.ProDownstreamRef
		Groups []int64
		Models []string
		Slots  int
	}{model.ProDownstream(p), p.Sub2.GroupIDs, p.Sub2.Models, p.Sub2.AccountConcurrency})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func restockPlan(provider, code, country string, quantity int) string {
	return fmt.Sprintf("%s:%s:%s:%d", provider, code, country, quantity)
}
func validateRestockTarget(c restock.Config, p model.ProSettings) error {
	if p.Provider != "sub2" {
		return errors.New("Pro 自动补货仅支持当前 Sub2 推送目标")
	}
	for _, g := range p.Sub2.GroupIDs {
		if g == c.GroupID {
			return nil
		}
	}
	return errors.New("监控分组必须包含在 Pro 推送目标分组中")
}
func (s *Server) startProRestockMonitor(ctx context.Context) {
	if saved, err := s.store.ProRestockSnapshot(); err == nil && saved.Valid && saved.LoadValid {
		s.proRestock.lastSnapshot = saved
		s.proRestock.quotaBinding = saved.Binding
		s.proRestock.quota = map[int64][]restock.Window{}
		s.proRestock.quotaPlans = map[int64]string{}
		for _, a := range saved.Accounts {
			s.proRestock.quota[a.ID] = a.Windows
			s.proRestock.quotaPlans[a.ID] = a.Plan
		}
	}
	s.proAutoWG.Add(1)
	go func() {
		defer s.proAutoWG.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickProRestock(ctx, false)
			}
		}
	}()
}
func (s *Server) collectProRestock(ctx context.Context, c restock.Config, p model.ProSettings, password string) (restock.Snapshot, error) {
	now := time.Now()
	snap := restock.Snapshot{ID: randomRegistrationID(), At: now, GroupID: c.GroupID, Binding: restockBinding(p), Accounts: []restock.Account{}}
	if e := validateRestockTarget(c, p); e != nil {
		return snap, e
	}
	groups, e := s.sub2.Groups(ctx, p.Sub2, password)
	if e != nil {
		return snap, e
	}
	found := false
	for _, g := range groups {
		if g.ID == c.GroupID {
			found = true
		}
	}
	if !found {
		return snap, errors.New("监控分组不存在或无法访问")
	}
	accounts, e := s.sub2.MonitorAccounts(ctx, p.Sub2, password, c.GroupID)
	if e != nil {
		return snap, e
	}
	loads, e := s.sub2.MonitorConcurrency(ctx, p.Sub2, password, c.GroupID)
	if e != nil {
		return snap, e
	}
	pay, _, payErr := s.store.GPTPaySettings()
	if payErr != nil {
		return snap, payErr
	}
	snap.NewPlan = restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)
	if p.ForceMergeEnabled {
		snap.LifetimeMinutes = p.ForceMergeMinutes
	}
	// Compare server timestamps with collection completion, not request start.
	now = time.Now()
	snap.At = loads.At
	snap.Valid = true
	snap.LoadValid = loads.Quality != nil && loads.Quality.Complete && loads.Quality.Expected == len(accounts) && loads.Quality.Succeeded == len(accounts) && loads.Quality.Failed == 0 && len(loads.Account) == len(accounts)
	if !snap.LoadValid {
		snap.Error = "Sub2 未提供完整采集质量证明；需支持 strict=true 和 quality 字段，当前仅观察"
	}
	profiles := s.store.MailAccountsByManagementScope("pro")
	byID := map[int64]model.MailAccountProfile{}
	for _, v := range profiles {
		if v.Sub2AccountID > 0 && v.ProAuto.RestockBinding == snap.Binding {
			byID[v.Sub2AccountID] = v
		}
	}
	s.proRestock.mu.Lock()
	if s.proRestock.quotaBinding != snap.Binding {
		s.proRestock.quota = map[int64][]restock.Window{}
		s.proRestock.quotaPlans = map[int64]string{}
		s.proRestock.quotaBinding = snap.Binding
		s.proRestock.quotaNext = time.Time{}
	}
	for _, a := range accounts {
		v := restock.Account{Fault: a.Status == "error" || (a.OverloadUntil != nil && a.OverloadUntil.After(now)) || (a.TempUntil != nil && a.TempUntil.After(now)), ID: a.ID, Name: a.Name, Plan: a.Credentials.Plan, Ready: a.Ready(now, p.Sub2.Models), Shared: len(a.GroupIDs) > 1, Capacity: a.Concurrency, Windows: append([]restock.Window{}, s.proRestock.quota[a.ID]...)}
		if a.ExpiresAt != nil {
			t := time.Unix(*a.ExpiresAt, 0)
			v.ExpiresAt = &t
		}
		if local, ok := byID[a.ID]; ok {
			if local.ProAuto.QuotaPlan != "" {
				v.Plan = local.ProAuto.QuotaPlan
			}
			if p.ForceMergeEnabled && local.ProPushStartedAt != nil && !local.SpaceMergedOnce {
				t := local.ProPushStartedAt.Add(time.Duration(p.ForceMergeMinutes) * time.Minute)
				if v.ExpiresAt == nil || t.Before(*v.ExpiresAt) {
					v.ExpiresAt = &t
				}
			}
		}
		if s.proRestock.quotaPlans[a.ID] != v.Plan {
			v.Windows = nil
		}
		load, ok := loads.Account[a.ID]
		if !ok || load.AccountID != a.ID || load.Used < 0 || load.Waiting < 0 || load.Known == nil || !*load.Known || load.Capacity != a.Concurrency {
			snap.LoadValid = false
			snap.Error = "账号成员、并发容量或采集质量不一致"
		} else {
			v.Used, v.Waiting = load.Used, load.Waiting
		}
		for _, w := range v.Windows {
			if w.Fresh(now, time.Duration(c.QuotaSeconds*2)*time.Second) && w.Used >= 100 {
				v.Ready = false
			}
		}
		snap.Accounts = append(snap.Accounts, v)
	}
	s.proRestock.mu.Unlock()
	if now.Sub(snap.At) > time.Duration(c.FreshSeconds)*time.Second || snap.At.After(now.Add(5*time.Second)) {
		snap.Valid = false
		snap.Error = "Sub2 快照时间过期或服务器时钟不一致"
	}
	s.refreshRestockQuota(c, p, password, snap)
	return snap, nil
}
func (s *Server) refreshRestockQuota(c restock.Config, p model.ProSettings, password string, snap restock.Snapshot) {
	s.proRestock.mu.Lock()
	if s.proRestock.quotaBusy || time.Now().Before(s.proRestock.quotaNext) {
		s.proRestock.mu.Unlock()
		return
	}
	s.proRestock.quotaBusy = true
	s.proRestock.quotaNext = time.Now().Add(time.Duration(c.QuotaSeconds) * time.Second)
	s.proRestock.mu.Unlock()
	s.proAutoMu.Lock()
	if s.proAutoClosed {
		s.proAutoMu.Unlock()
		s.proRestock.mu.Lock()
		s.proRestock.quotaBusy = false
		s.proRestock.mu.Unlock()
		return
	}
	s.proAutoWG.Add(1)
	s.proAutoMu.Unlock()
	go func() {
		defer s.proAutoWG.Done()
		defer func() { s.proRestock.mu.Lock(); s.proRestock.quotaBusy = false; s.proRestock.mu.Unlock() }()
		jobs := make(chan restock.Account)
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for a := range jobs {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					q, e := s.sub2.QueryQuota(ctx, p.Sub2, password, a.ID)
					cancel()
					if e != nil {
						continue
					}
					f, se := q.Windows()
					windows := []restock.Window{}
					for _, w := range []*model.FreeQuotaWindow{f, se} {
						if w != nil {
							at := time.Now()
							reset := w.ResetAt
							if reset == 0 && w.ResetAfterSeconds > 0 {
								reset = at.Unix() + w.ResetAfterSeconds
							}
							windows = append(windows, restock.Window{Seconds: w.LimitWindowSeconds, ResetAt: reset, Used: w.UsedPercent, At: at})
						}
					}
					windows = restock.ObserveWindows(windows, a.Windows, true, time.Duration(c.QuotaSeconds*3)*time.Second)
					s.proRestock.mu.Lock()
					if s.proRestock.quotaBinding == snap.Binding {
						s.proRestock.quota[a.ID] = windows
						s.proRestock.quotaPlans[a.ID] = a.Plan
					}
					s.proRestock.mu.Unlock()
				}
			}()
		}
		for _, a := range snap.Accounts {
			s.proAutoMu.Lock()
			closed := s.proAutoClosed
			s.proAutoMu.Unlock()
			if closed {
				break
			}
			jobs <- a
		}
		close(jobs)
		wg.Wait()
	}()
}
