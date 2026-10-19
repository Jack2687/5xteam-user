package restock

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// TakeoverConfig is separate from the purchasing policy. A peer can never
// rewrite that policy, credentials, daily merge settings or payment settings.
type TakeoverConfig struct {
	Version             int     `json:"version"`
	Enabled             bool    `json:"enabled"`
	Role                string  `json:"role"`
	PeerURL             string  `json:"peer_url"`
	Username            string  `json:"username"`
	PasswordPresent     bool    `json:"password_present"`
	PeerID              string  `json:"peer_id"`
	Groups              []int64 `json:"groups"`
	LeadMinutes         int     `json:"lead_minutes"`
	CheckSeconds        int     `json:"check_seconds"`
	SupportMinAccounts  int     `json:"support_min_accounts"`
	ReturnMinAccounts   int     `json:"return_min_accounts"`
	ReturnConfirmations int     `json:"return_confirmations"`
}

func DefaultTakeoverConfig() TakeoverConfig {
	return TakeoverConfig{Version: 1, Role: "receiver", LeadMinutes: 10, CheckSeconds: 10, SupportMinAccounts: 1, ReturnMinAccounts: 1, ReturnConfirmations: 3, Groups: []int64{}}
}
func (c TakeoverConfig) Validate() error {
	if c.Role != "receiver" && c.Role != "provider" {
		return errors.New("请选择接受接管或提供接管")
	}
	if c.LeadMinutes < 0 || c.LeadMinutes > 120 || c.CheckSeconds < 5 || c.CheckSeconds > 60 || c.SupportMinAccounts < 1 || c.SupportMinAccounts > 1000 || c.ReturnMinAccounts < 1 || c.ReturnMinAccounts > 1000 || c.ReturnConfirmations < 1 || c.ReturnConfirmations > 20 {
		return errors.New("提前准备 0～120 分钟，检查 5～60 秒，恢复账号 1～1000，确认 1～20 次")
	}
	if c.PeerURL != "" {
		u, e := url.Parse(c.PeerURL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("对方地址应为完整的 HTTP/HTTPS 地址，不能包含账密、查询参数或片段")
		}
	}
	if c.Enabled && (c.PeerURL == "" || strings.TrimSpace(c.Username) == "" || c.PeerID == "") {
		return errors.New("请填写对方地址、登录账号和密码，并测试连接")
	}
	if len(c.Groups) > 20 {
		return errors.New("接管分组最多 20 个")
	}
	seen := map[int64]bool{}
	for _, g := range c.Groups {
		if g <= 0 || seen[g] {
			return errors.New("接管分组无效或重复")
		}
		seen[g] = true
	}
	if c.Enabled && c.Role == "receiver" && len(c.Groups) == 0 {
		return errors.New("请选择需要接管的分组")
	}
	return nil
}

// The receiver owns the handoff epoch and bounded grant. Expiration never
// removes group membership; withdrawal needs an explicit fresh release.
type TakeoverState struct {
	Epoch           string    `json:"epoch"`
	Phase           string    `json:"phase"`
	PeerID          string    `json:"peer_id"`
	Groups          []int64   `json:"groups"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	LeaseUntil      time.Time `json:"lease_until"`
	ContactAt       time.Time `json:"contact_at"`
	RecoveryAt      time.Time `json:"recovery_at"`
	LastReadySample time.Time `json:"last_ready_sample"`
	ReadyCount      int       `json:"ready_count"`
	OwnReady        int       `json:"own_ready"`
	OwnCapacity     float64   `json:"own_capacity"`
	Demand          int       `json:"demand"`
	Pending         int       `json:"pending"`
	PeerPending     int       `json:"peer_pending"`
	CoverageReady   bool      `json:"coverage_ready"`
	Covered         int       `json:"covered"`
	Error           string    `json:"error"`
}

func (s TakeoverState) Active() bool { return s.Epoch != "" && s.Phase != "idle" }
func (s TakeoverState) Taking() bool { return s.Phase == "preparing" || s.Phase == "active" }
func (s TakeoverState) Token(c TakeoverConfig) string {
	if !s.Active() {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", c.Role, s.Epoch, s.Phase)
}
func (s TakeoverState) Block(c TakeoverConfig, now time.Time) string {
	if !s.Active() {
		return ""
	}
	if c.Role == "receiver" {
		if s.Taking() {
			return "对方接管中，暂停本项目新增；原在途与额度巡检继续"
		}
		if s.Phase == "recovering" && (now.Before(s.LeaseUntil) || s.PeerPending > 0 || now.Sub(s.ContactAt) > time.Minute) {
			return "等待对方在途结束并确认交还补货权"
		}
	} else if s.Taking() && (!s.CoverageReady || !c.Enabled || !now.Before(s.LeaseUntil) || now.Sub(s.ContactAt) > time.Minute) {
		return "等待分组就绪或接管授权续期，保留现有服务"
	}
	return ""
}

// Find the continuous FULL prohibition containing now or beginning within
// lead. Minute enumeration naturally handles overlap and midnight. Allowed
// issuer exceptions are not full prohibition and do not trigger a handoff.
func (c Config) TakeoverWindow(now time.Time, lead int) (start, end time.Time, ok bool) {
	if !c.NoRestockEnabled || c.validateNoRestockWindows() != nil {
		return
	}
	minute := now.In(scheduleLocation).Truncate(time.Minute)
	at := minute
	if !c.CardSchedule(at).Blocked {
		found := false
		for i := 1; i <= lead; i++ {
			t := minute.Add(time.Duration(i) * time.Minute)
			if c.CardSchedule(t).Blocked {
				at = t
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	start = at
	for i := 0; i < 1440 && c.CardSchedule(start.Add(-time.Minute)).Blocked; i++ {
		start = start.Add(-time.Minute)
	}
	end = at.Add(time.Minute)
	for i := 0; i < 1440 && c.CardSchedule(end).Blocked; i++ {
		end = end.Add(time.Minute)
	}
	return start, end, true
}

func TakeoverRecoveryReady(c Config, t TakeoverConfig, s Snapshot, pending int, after, now time.Time) (bool, int, float64, int) {
	count, reported, demand := 0, 0, 0
	for _, a := range s.Accounts {
		demand += a.Used + a.Waiting
		if !availableAccount(a, now) {
			continue
		}
		if a.ForceMergeDeadline != nil && a.ForceMergeDeadline.Before(now.Add(time.Duration(c.LeadMinutes+c.MarginMinutes)*time.Minute)) {
			continue
		}
		count++
		reported += a.Capacity
	}
	capacity := c.EffectiveCapacity(reported)
	ready := s.Valid && s.LoadValid && !s.CollectionStartedAt.IsZero() && s.RefreshedAfter(after) && now.Sub(s.At) <= time.Duration(c.FreshSeconds)*time.Second && !s.At.After(now.Add(5*time.Second)) && pending == 0 && count >= t.ReturnMinAccounts && float64(demand) <= capacity*float64(c.TargetPercent)/100
	if ready && c.TimeEnabled && demand > 0 {
		f := ForecastMoney(c, s, now)
		ready = f.State == "ready" && f.RemainingUSD != nil && f.RateUSD != nil && (*f.RateUSD == 0 || *f.RemainingUSD >= *f.RateUSD*float64(c.TargetMinutes))
	}
	return ready, count, capacity, demand
}

func TakeoverGroups(base int64, c TakeoverConfig, s TakeoverState) []int64 {
	groups := []int64{base}
	if s.Active() && ((c.Role == "provider" && s.Taking()) || c.Role == "receiver") {
		for _, g := range s.Groups {
			if !slices.Contains(groups, g) {
				groups = append(groups, g)
			}
		}
	}
	slices.Sort(groups)
	return groups
}
