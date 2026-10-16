// Package restock contains the side-effect-free Pro replenishment policy.
package restock

import (
	"errors"
	"time"
)

type Config struct {
	NoRestockEnabled         bool   `json:"no_restock_enabled"`
	NoRestockStart           string `json:"no_restock_start"`
	NoRestockEnd             string `json:"no_restock_end"`
	ForceMergeEnabled        bool   `json:"force_merge_enabled"`
	ForceMergeMinutes        int    `json:"force_merge_minutes"`
	MinNewUsePercent         int    `json:"min_new_use_percent"`
	FaultPercent             int    `json:"fault_percent"`
	Version                  int    `json:"version"`
	Enabled                  bool   `json:"enabled"`
	Mode                     string `json:"mode"`
	GroupID                  int64  `json:"group_id"`
	TimeEnabled              bool   `json:"time_enabled"`
	TriggerMinutes           int    `json:"trigger_minutes"`
	TargetMinutes            int    `json:"target_minutes"`
	Early                    bool   `json:"early"`
	ConcurrencyEnabled       bool   `json:"concurrency_enabled"`
	TriggerPercent           int    `json:"trigger_percent"`
	TargetPercent            int    `json:"target_percent"`
	Confirmations            int    `json:"confirmations"`
	EmergencyQueue           int    `json:"emergency_queue"`
	MinBatch                 int    `json:"min_batch"`
	MaxBatch                 int    `json:"max_batch"`
	AllowSmall               bool   `json:"allow_small"`
	MaxInflight              int    `json:"max_inflight"`
	Reserve                  int    `json:"reserve"`
	EmptyGroupCount          int    `json:"empty_group_count"`
	CheckSeconds             int    `json:"check_seconds"`
	QuotaSeconds             int    `json:"quota_seconds"`
	CooldownSeconds          int    `json:"cooldown_seconds"`
	EmergencyCooldownSeconds int    `json:"emergency_cooldown_seconds"`
	HourLimit                int    `json:"hour_limit"`
	DayLimit                 int    `json:"day_limit"`
	HourBudget               int64  `json:"hour_budget"`
	DayBudget                int64  `json:"day_budget"`
	UnitCost                 int64  `json:"unit_cost"`
	PoolLimit                int    `json:"pool_limit"`
	FailureLimit             int    `json:"failure_limit"`
	FreshSeconds             int    `json:"fresh_seconds"`
	QuotaCoverage            int    `json:"quota_coverage"`
	LeadMinutes              int    `json:"lead_minutes"`
	MarginMinutes            int    `json:"margin_minutes"`
	ForecastMinutes          int    `json:"forecast_minutes"`
	LeadSamples              int    `json:"lead_samples"`
	RetentionDays            int    `json:"retention_days"`
}

func DefaultConfig() Config {
	return Config{NoRestockStart: "00:00", NoRestockEnd: "07:00", ForceMergeMinutes: 15, FaultPercent: 50, Version: 1, Mode: "observe", TimeEnabled: true, TriggerMinutes: 10, TargetMinutes: 15, Early: true, ConcurrencyEnabled: true, TriggerPercent: 85, TargetPercent: 75, Confirmations: 2, EmergencyQueue: 3, MinBatch: 3, MaxBatch: 5, AllowSmall: true, MaxInflight: 6, CheckSeconds: 5, QuotaSeconds: 60, CooldownSeconds: 60, EmergencyCooldownSeconds: 20, HourLimit: 20, DayLimit: 100, PoolLimit: 100, FailureLimit: 3, FreshSeconds: 20, QuotaCoverage: 80, LeadMinutes: 5, MarginMinutes: 1, ForecastMinutes: 60, LeadSamples: 10, RetentionDays: 7}
}
func (c Config) Validate() error {
	if c.NoRestockEnabled {
		start, startOK := dailyMinute(c.NoRestockStart)
		end, endOK := dailyMinute(c.NoRestockEnd)
		if !startOK || !endOK {
			return errors.New("每日禁补开始和结束时间必须为 HH:mm（00:00～23:59）")
		}
		if start == end {
			return errors.New("每日禁补开始和结束时间不能相同；全天停止请关闭自动补货总开关")
		}
	}
	if c.ForceMergeMinutes < 1 || c.ForceMergeMinutes > 525600 {
		return errors.New("提前补货平均剩余时间阈值应为 1～525600 分钟")
	}
	if c.EmptyGroupCount < 0 || c.EmptyGroupCount > 1000 {
		return errors.New("空分组启动补货数量应为 0～1000，0 表示关闭")
	}
	if c.MinNewUsePercent < 0 || c.MinNewUsePercent > 100 {
		return errors.New("新号预计最低使用比例应为 0～100%，0 表示不限制")
	}
	if c.FaultPercent < 1 || c.FaultPercent > 100 {
		return errors.New("账号异常暂停比例应为 1～100%")
	}
	if c.Mode != "observe" && c.Mode != "auto" {
		return errors.New("运行模式无效")
	}
	if c.Enabled && c.GroupID <= 0 {
		return errors.New("请选择唯一监控分组")
	}
	if c.TriggerMinutes < 1 || c.TargetMinutes <= c.TriggerMinutes || c.TargetMinutes > 1440 || c.ForecastMinutes < c.TargetMinutes || c.ForecastMinutes > 1440 {
		return errors.New("目标分钟必须大于触发分钟，且不超过预测范围（最多 1440 分钟）")
	}
	if c.TriggerPercent < 1 || c.TriggerPercent > 100 || c.TargetPercent < 1 || c.TargetPercent >= c.TriggerPercent || c.Confirmations < 1 || c.Confirmations > 100 || c.EmergencyQueue < 1 {
		return errors.New("并发阈值、目标或确认次数无效")
	}
	if c.MinBatch < 1 || c.MaxBatch < c.MinBatch || c.MaxBatch > 100 || c.MaxInflight < c.MinBatch || c.MaxInflight > 1000 || c.Reserve < 0 || c.Reserve > c.PoolLimit {
		return errors.New("批量、在途或备用上限无效")
	}
	if c.CheckSeconds < 2 || c.CheckSeconds > 300 || c.QuotaSeconds < 30 || c.QuotaSeconds > 3600 || c.CooldownSeconds < 1 || c.CooldownSeconds > 86400 || c.EmergencyCooldownSeconds < 1 || c.EmergencyCooldownSeconds > c.CooldownSeconds {
		return errors.New("检查周期或冷却时间无效")
	}
	if c.HourLimit < 1 || c.DayLimit < c.HourLimit || c.DayLimit > 10000 || c.PoolLimit < 1 || c.PoolLimit > 10000 || c.FailureLimit < 1 || c.FailureLimit > 100 || c.HourBudget < 0 || c.DayBudget < 0 || c.UnitCost < 0 || c.UnitCost > 100000000 || c.HourBudget > 100000000000 || c.DayBudget > 100000000000 {
		return errors.New("预算、数量限制或熔断参数无效")
	}
	if (c.HourBudget > 0 || c.DayBudget > 0) && c.UnitCost <= 0 {
		return errors.New("设置金额预算时必须填写每号成本预留，使用与预算一致的记账单位")
	}
	if c.FreshSeconds < c.CheckSeconds || c.FreshSeconds > 3600 || c.QuotaCoverage < 1 || c.QuotaCoverage > 100 || c.LeadMinutes < 1 || c.LeadMinutes > 1440 || c.MarginMinutes < 0 || c.MarginMinutes > 120 || c.LeadSamples < 2 || c.LeadSamples > 1000 || c.RetentionDays < 1 || c.RetentionDays > 90 {
		return errors.New("数据保护或就绪耗时参数无效")
	}
	return nil
}

type Window struct {
	Seconds       int64     `json:"seconds"`
	ResetAt       int64     `json:"reset_at"`
	Used          float64   `json:"used"`
	Rate          float64   `json:"rate"`                    // percentage points/minute, same plan/window only
	ObservedRate  *float64  `json:"observed_rate,omitempty"` // latest interval, before conservative smoothing
	SampleSeconds float64   `json:"sample_seconds,omitempty"`
	Samples       int       `json:"samples"`
	At            time.Time `json:"at"`
}

// Fresh prevents old quota samples from reducing current or future capacity.
func (w Window) Fresh(now time.Time, maxAge time.Duration) bool {
	return !w.At.IsZero() && !w.At.After(now.Add(5*time.Second)) && now.Sub(w.At) <= maxAge && w.Seconds > 0 && w.ResetAt > now.Unix() && validWindow(w.Used, w.Rate)
}

type Account struct {
	Money              *AccountMoney `json:"money,omitempty"`
	PurchasePlan       string        `json:"purchase_plan,omitempty"`
	EJH                bool          `json:"ejh"`
	ForceMergeKey      string        `json:"force_merge_key,omitempty"`
	PushTimeoutEnabled *bool         `json:"push_timeout_enabled,omitempty"`
	ForceMergeDeadline *time.Time    `json:"force_merge_deadline,omitempty"`
	Fault              bool          `json:"fault"`
	ID                 int64         `json:"id"`
	Name               string        `json:"name"`
	Plan               string        `json:"plan"`
	Ready              bool          `json:"ready"`
	Shared             bool          `json:"shared"`
	Capacity           int           `json:"capacity"`
	Used               int           `json:"used"`
	Waiting            int           `json:"waiting"`
	ExpiresAt          *time.Time    `json:"expires_at,omitempty"`
	Windows            []Window      `json:"windows"`
}
type Snapshot struct {
	NewPlan         string    `json:"new_plan"`
	LifetimeMinutes int       `json:"lifetime_minutes"`
	ID              string    `json:"id"`
	At              time.Time `json:"at"`
	GroupID         int64     `json:"group_id"`
	Binding         string    `json:"binding"`
	Valid           bool      `json:"valid"`
	LoadValid       bool      `json:"load_valid"`
	Error           string    `json:"error"`
	Accounts        []Account `json:"accounts"`
}
type Task struct {
	ManagedExternally bool                 `json:"managed_externally,omitempty"`
	ForceMergeSource  string               `json:"force_merge_source,omitempty"`
	EmptyGroupStartup bool                 `json:"empty_group_startup,omitempty"`
	Plan              string               `json:"plan"`
	ID                string               `json:"id"`
	DecisionID        string               `json:"decision_id"`
	Email             string               `json:"email"`
	Version           int                  `json:"version"`
	GroupID           int64                `json:"group_id"`
	Binding           string               `json:"binding"`
	Status            string               `json:"status"`
	Stage             string               `json:"stage"`
	Message           string               `json:"message"`
	OrderID           string               `json:"order_id"`
	CardID            string               `json:"card_id"`
	AccountID         int64                `json:"account_id"`
	Cost              int64                `json:"cost"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
	SubmittedAt       *time.Time           `json:"submitted_at,omitempty"`
	ReadyAt           *time.Time           `json:"ready_at,omitempty"`
	ETA               *time.Time           `json:"eta,omitempty"`
	Stages            map[string]time.Time `json:"stages"`
}

func (t Task) Active() bool {
	return t.Status != "ready" && t.Status != "failed" && t.Status != "cancelled"
}
func (t Task) Predictable() bool {
	return t.Active() && t.ETA != nil && t.Status != "unknown" && t.Status != "attention"
}

type Limits struct {
	HourCount int        `json:"hour_count"`
	DayCount  int        `json:"day_count"`
	HourCost  int64      `json:"hour_cost"`
	DayCost   int64      `json:"day_cost"`
	Failures  int        `json:"failures"`
	LastBatch *time.Time `json:"last_batch,omitempty"`
}
type Decision struct {
	Money           MoneyForecast   `json:"money"`
	NoRestockActive bool            `json:"no_restock_active"`
	ForceMerge      ForceMergeStats `json:"force_merge"`
	ForceMergeNeed  int             `json:"force_merge_need"`
	DemandKnown     bool            `json:"demand_known"`
	IdleEquivalent  float64         `json:"idle_equivalent"`
	ID              string          `json:"id"`
	At              time.Time       `json:"at"`
	Version         int             `json:"version"`
	Snapshot        Snapshot        `json:"snapshot"`
	Config          Config          `json:"config"`
	Limits          Limits          `json:"limits"`
	Reason          string          `json:"reason"`
	Warnings        []string        `json:"warnings"`
	SupplyMinutes   *float64        `json:"supply_minutes"`
	SupplyCapped    bool            `json:"supply_capped"`
	Coverage        float64         `json:"coverage"`
	Usable          int             `json:"usable"`
	Capacity        int             `json:"capacity"`
	Used            int             `json:"used"`
	Waiting         int             `json:"waiting"`
	Inflight        int             `json:"inflight"`
	LeadMinutes     float64         `json:"lead_minutes"`
	TriggerMinutes  float64         `json:"trigger_minutes"`
	TargetMinutes   float64         `json:"target_minutes"`
	TimeNeed        int             `json:"time_need"`
	ConcurrencyNeed int             `json:"concurrency_need"`
	ReserveNeed     int             `json:"reserve_need"`
	EmptyGroupNeed  int             `json:"empty_group_need"`
	Suggested       int             `json:"suggested"`
	Started         int             `json:"started"`
	Confirmations   int             `json:"confirmations"`
}

// EmptyGroupOnly identifies an explicit startup quantity, which must not be
// rounded up to the ordinary minimum batch size.
func (d Decision) EmptyGroupOnly() bool {
	return d.EmptyGroupNeed > 0 && d.ForceMergeNeed == 0 && d.TimeNeed == 0 && d.ConcurrencyNeed == 0 && d.ReserveNeed == 0
}
