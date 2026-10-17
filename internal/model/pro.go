package model

import (
	"fmt"
	"time"
)

type ProAutoState struct {
	RetryAttempts        int                  `json:"retry_attempts,omitempty"`
	RetryAt              *time.Time           `json:"retry_at,omitempty"`
	StopVersion          uint64               `json:"stop_version,omitempty"`
	StopRequested        bool                 `json:"stop_requested,omitempty"`
	RestockRecoveries    int                  `json:"restock_recoveries,omitempty"`
	RestockRecoveryAt    *time.Time           `json:"restock_recovery_at,omitempty"`
	RestockBinding       string               `json:"restock_binding,omitempty"`
	QuotaPlan            string               `json:"quota_plan,omitempty"`
	StageTimes           map[string]time.Time `json:"stage_times,omitempty"`
	ProvisionedAt        *time.Time           `json:"provisioned_at,omitempty"`
	ID                   string               `json:"id,omitempty"`
	Status               string               `json:"status,omitempty"`
	Stage                string               `json:"stage,omitempty"`
	Steps                map[string]string    `json:"steps,omitempty"`
	Error                string               `json:"error,omitempty"`
	OrderID              string               `json:"order_id,omitempty"`
	PlanCode             string               `json:"plan_code,omitempty"`
	CardID               string               `json:"card_id,omitempty"`
	ProxyName            string               `json:"proxy_name,omitempty"`
	ExitIP               string               `json:"exit_ip,omitempty"`
	QuotaUsedThreshold   float64              `json:"quota_used_threshold"`
	QuotaIntervalSeconds int                  `json:"quota_interval_seconds"`
	StartedAt            time.Time            `json:"started_at,omitempty"`
	UpdatedAt            time.Time            `json:"updated_at,omitempty"`
	NextCheckAt          *time.Time           `json:"next_check_at,omitempty"`
}

// Each confirmed unpaid retry gets a new payment key; the workflow and
// restock task keep their identity. Recovery must use this key after a crash.
func (s ProAutoState) PaymentOrderID() string {
	if s.ID == "" {
		return ""
	}
	id := "pro-auto-" + s.ID
	if s.RetryAttempts > 0 {
		id += fmt.Sprintf("-retry-%d", s.RetryAttempts)
	}
	return id
}

// ProSettings is intentionally independent from Team rotation push settings.
// Its providers are mutually exclusive through Provider.
type ProSettings struct {
	EJHAutoOpen               bool           `json:"ejh_auto_open"`
	EJHTemplateID             string         `json:"ejh_template_id"`
	EJHDailyOpenLimit         int            `json:"ejh_daily_open_limit"`
	EJHDailyAmountLimit       string         `json:"ejh_daily_amount_limit"`
	ForceMergeEnabled         bool           `json:"force_merge_enabled"`
	ForceMergeMinutes         int            `json:"force_merge_minutes"`
	PostMergeGroupIDs         []int64        `json:"post_merge_group_ids"`
	PostMergeGroupNames       []string       `json:"post_merge_group_names"`
	ScheduledEnabled          bool           `json:"scheduled_enabled"`
	MaxUnmerged               int            `json:"max_unmerged"`
	ScheduleIntervalSeconds   int            `json:"schedule_interval_seconds"`
	Provider                  string         `json:"provider"`
	Sub2                      Sub2Settings   `json:"sub2"`
	CPA                       CPASettings    `json:"cpa"`
	VM2API                    VM2APISettings `json:"vm2api"`
	QuotaEnabled              bool           `json:"quota_enabled"`
	QuotaUsedThreshold        float64        `json:"quota_used_threshold"`
	QuotaCheckIntervalSeconds int            `json:"quota_check_interval_seconds"`
	AutoMergeEnabled          bool           `json:"auto_merge_enabled"`
	TargetAdminID             string         `json:"target_admin_id,omitempty"`
	TargetSeatType            string         `json:"target_seat_type"`
	RetryCount                int            `json:"retry_count"`
	RetryIntervalSeconds      int            `json:"retry_interval_seconds"`
	Concurrency               int            `json:"concurrency"`
	MergeConcurrency          int            `json:"merge_concurrency"`
	LastQuotaSweepAt          *time.Time     `json:"last_quota_sweep_at,omitempty"`
	NextQuotaSweepAt          *time.Time     `json:"next_quota_sweep_at,omitempty"`
}

func DefaultProSettings() ProSettings {
	return ProSettings{
		EJHDailyOpenLimit: 1, ForceMergeMinutes: 60,
		MaxUnmerged: 5, ScheduleIntervalSeconds: 120,
		Provider: "sub2", Sub2: DefaultSub2Settings(), CPA: DefaultCPASettings(), VM2API: DefaultVM2APISettings(),
		QuotaUsedThreshold: 100, QuotaCheckIntervalSeconds: 120, TargetSeatType: "default",
		RetryCount: 2, RetryIntervalSeconds: 3, Concurrency: 2, MergeConcurrency: 2,
	}
}

type ProOAuthSession struct {
	ID          string    `json:"session_id"`
	State       string    `json:"state"`
	TargetEmail string    `json:"target_email,omitempty"`
	RedirectURI string    `json:"redirect_uri"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
