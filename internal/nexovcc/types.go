package nexovcc

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const BaseURL = "https://nexovcc.com"

type Credentials struct {
	Login        string `json:"login"`
	Password     string `json:"password"`
	FundPassword string `json:"fund_password"`
	TOTPSecret   string `json:"totp_secret"`
}
type Settings struct {
	Enabled                     bool       `json:"enabled"`
	LoginPresent                bool       `json:"login_present"`
	PasswordPresent             bool       `json:"password_present"`
	FundPasswordPresent         bool       `json:"fund_password_present"`
	TOTPPresent                 bool       `json:"totp_present"`
	DefaultMaxAccounts          int        `json:"default_max_accounts"`
	SingleActiveAccount         bool       `json:"single_active_account"`
	PageRefreshSeconds          int        `json:"page_refresh_seconds"`
	BalanceSyncSeconds          int        `json:"balance_sync_seconds"`
	CardSyncSeconds             int        `json:"card_sync_seconds"`
	TransactionSyncSeconds      int        `json:"transaction_sync_seconds"`
	AccountSyncSeconds          int        `json:"account_sync_seconds"`
	ProductSyncSeconds          int        `json:"product_sync_seconds"`
	PageAutoRefresh             bool       `json:"page_auto_refresh"`
	HTTP522RetrySeconds         int        `json:"http_522_retry_seconds"` // Shared by HTTP 500/522; preserve saved keys.
	HTTP522Retries              int        `json:"http_522_retries"`
	RequestIntervalSeconds      int        `json:"request_interval_seconds"`
	MaxOnlineCards              int        `json:"max_online_cards"`
	AutoRecycleAt               int        `json:"auto_recycle_at"`
	AutoWithdrawEnabled         bool       `json:"auto_withdraw_enabled"`
	AutoWithdrawIntervalSeconds int        `json:"auto_withdraw_interval_seconds"`
	BatchIntervalSeconds        int        `json:"batch_interval_seconds"`
	Templates                   []Template `json:"templates"`
}
type Template struct {
	PlanCode   string   `json:"plan_code,omitempty"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ProductID  string   `json:"product_id"`
	ProductIDs []string `json:"product_ids"`
	Amount     string   `json:"amount"`
	FeeBuffer  string   `json:"fee_buffer"`
	HolderID   string   `json:"holder_id"`
}
type Product struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BIN          string `json:"bin"`
	Organization string `json:"organization"`
	Minimum      string `json:"minimum"`
}
type Balance struct {
	Amount string    `json:"amount"`
	Frozen string    `json:"frozen"`
	At     time.Time `json:"at"`
}
type Card struct {
	ID              string     `json:"id"`
	Serial          string     `json:"serial_no"`
	Status          string     `json:"status"`
	Last4           string     `json:"last4"`
	BIN             string     `json:"bin"`
	ProductID       string     `json:"product_id"`
	Remark          string     `json:"remark"`
	Balance         string     `json:"balance"`
	BalanceAt       time.Time  `json:"balance_at"`
	CreatedAt       time.Time  `json:"created_at"`
	SyncedAt        time.Time  `json:"synced_at"`
	Imported        bool       `json:"imported"`
	Error           string     `json:"error"`
	Control         string     `json:"control,omitempty"`
	ControlAt       time.Time  `json:"control_at,omitempty"`
	NormalPending   bool       `json:"normal_pending,omitempty"`
	NormalPendingAt *time.Time `json:"normal_pending_at,omitempty"`
	ClosedAt        time.Time  `json:"closed_at,omitempty"`
}
type PrivateCard struct {
	Number      string
	CVV         string
	Month, Year int
}
type Operation struct {
	RestockTaskID        string             `json:"restock_task_id,omitempty"`
	PlanCode             string             `json:"plan_code,omitempty"`
	ID                   string             `json:"id"`
	Action               string             `json:"action"`
	CardID               string             `json:"card_id"`
	Serial               string             `json:"serial_no"`
	TemplateID           string             `json:"template_id"`
	ProductID            string             `json:"product_id"`
	Amount               string             `json:"amount"`
	Remark               string             `json:"remark"`
	Status               string             `json:"status"`
	Error                string             `json:"error"`
	Evidence             string             `json:"evidence"`
	Automatic            bool               `json:"automatic"`
	CapacityRecycle      bool               `json:"capacity_recycle,omitempty"`
	AutoWithdraw         bool               `json:"auto_withdraw,omitempty"`
	BalanceWithdrawal    *BalanceWithdrawal `json:"balance_withdrawal,omitempty"`
	BeforeBalance        string             `json:"before_balance"`
	BeforeAccountBalance string             `json:"before_account_balance"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
	Step                 string             `json:"step,omitempty"`
	StepAt               time.Time          `json:"step_at,omitempty"`
	WithdrawAmount       string             `json:"withdraw_amount,omitempty"`
	WithdrawEvidence     string             `json:"withdraw_evidence,omitempty"`
	FrozenAt             time.Time          `json:"frozen_at,omitempty"`
}

func (o Operation) Terminal() bool { return o.Status == "succeeded" || o.Status == "failed" }
func Defaults() Settings {
	return Settings{DefaultMaxAccounts: 1, SingleActiveAccount: true, PageRefreshSeconds: 30, PageAutoRefresh: true, HTTP522RetrySeconds: 5, HTTP522Retries: 2, RequestIntervalSeconds: 3, AutoWithdrawIntervalSeconds: 60, BatchIntervalSeconds: 3, Templates: []Template{}}
}

var moneyPattern = regexp.MustCompile(`^\d{1,10}(\.\d{1,2})?$`)
var requestPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{7,31}$`)

func ValidRequestID(s string) bool {
	return requestPattern.MatchString(s) && strings.ContainsAny(s, "0123456789")
}
func Cents(s string) (int64, error) {
	if !moneyPattern.MatchString(s) {
		return 0, fmt.Errorf("金额须为非负数，最多两位小数")
	}
	parts := strings.SplitN(s, ".", 2)
	n, _ := strconv.ParseInt(parts[0], 10, 64)
	frac := "00"
	if len(parts) == 2 {
		frac = (parts[1] + "00")[:2]
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	return n*100 + f, nil
}
func Money(n int64) string { return fmt.Sprintf("%d.%02d", n/100, n%100) }
func Normalize(s Settings) (Settings, error) {
	s = s.WithSyncDefaults()
	for _, item := range []struct {
		name    string
		seconds int
	}{
		{"主账户余额", s.BalanceSyncSeconds}, {"卡片资料与余额", s.CardSyncSeconds},
		{"卡片交易与资金流水", s.TransactionSyncSeconds}, {"充值记录与每日汇总", s.AccountSyncSeconds}, {"产品与 BIN", s.ProductSyncSeconds},
	} {
		if item.seconds < 60 || item.seconds > 86400 {
			return s, fmt.Errorf("%s同步间隔须为 60～86400 秒", item.name)
		}
	}
	if s.HTTP522RetrySeconds == 0 {
		s.HTTP522RetrySeconds = 5
	}
	if s.HTTP522RetrySeconds < 1 || s.HTTP522RetrySeconds > 300 || s.HTTP522Retries < 0 || s.HTTP522Retries > 10 {
		return s, fmt.Errorf("500 / 522 重试间隔须为 1～300 秒，重试次数须为 0～10 次")
	}
	if s.AutoWithdrawIntervalSeconds == 0 {
		s.AutoWithdrawIntervalSeconds = 60
	}
	if s.AutoWithdrawIntervalSeconds < 30 || s.AutoWithdrawIntervalSeconds > 86400 {
		return s, fmt.Errorf("自动提现检查间隔须为 30～86400 秒")
	}
	if s.RequestIntervalSeconds == 0 {
		s.RequestIntervalSeconds = 3
	}
	if s.RequestIntervalSeconds < 1 || s.RequestIntervalSeconds > 30 {
		return s, fmt.Errorf("接口请求间隔须为 1～30 秒")
	}
	if s.BatchIntervalSeconds == 0 {
		s.BatchIntervalSeconds = 3 // Existing clients/configurations omit this field.
	}
	if s.BatchIntervalSeconds < 1 || s.BatchIntervalSeconds > 300 {
		return s, fmt.Errorf("批量卡片间隔须为 1～300 秒")
	}
	if s.AutoRecycleAt < 0 || s.AutoRecycleAt > 10000 {
		return s, fmt.Errorf("自动回收触发卡数须为 0～10000，0 表示关闭")
	}
	if s.MaxOnlineCards < 0 || s.MaxOnlineCards > 10000 {
		return s, fmt.Errorf("最多在线卡数须为 0～10000，0 表示不限制")
	}
	if s.DefaultMaxAccounts < 1 || s.DefaultMaxAccounts > 10000 {
		return s, fmt.Errorf("每卡开通上限须为 1～10000")
	}
	if s.PageRefreshSeconds < 5 || s.PageRefreshSeconds > 86400 {
		return s, fmt.Errorf("刷新间隔须为 5～86400 秒")
	}
	if len(s.Templates) > 100 {
		return s, fmt.Errorf("模板不能超过 100 个")
	}
	seen := map[string]bool{}
	for i := range s.Templates {
		t := &s.Templates[i]
		if t.PlanCode != "" && t.PlanCode != "pro5" && t.PlanCode != "pro20" {
			return s, fmt.Errorf("模板适用套餐须为 5x 或 20x")
		}
		t.Name = strings.TrimSpace(t.Name)
		t.ProductIDs = t.OrderedProductIDs()
		amount, ae := Cents(t.Amount)
		if t.ID == "" || len(t.ID) > 100 || seen[t.ID] || t.Name == "" || len([]rune(t.Name)) > 60 || len(t.ProductIDs) == 0 || len(t.ProductIDs) > 50 || ae != nil || amount <= 0 {
			return s, fmt.Errorf("请完整填写模板名称、产品和开卡金额")
		}
		products := map[string]bool{}
		for j, id := range t.ProductIDs {
			pid, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
			if err != nil || pid < 1 {
				return s, fmt.Errorf("模板产品 ID 无效")
			}
			id = strconv.FormatInt(pid, 10)
			if products[id] {
				return s, fmt.Errorf("模板产品不能重复选择")
			}
			products[id], t.ProductIDs[j] = true, id
		}
		t.ProductID = t.ProductIDs[0] // Compatibility with existing clients/exports.
		seen[t.ID] = true
		if t.FeeBuffer == "" {
			t.FeeBuffer = "0.00"
		}
		if _, e := Cents(t.FeeBuffer); e != nil {
			return s, e
		}
		if t.HolderID != "" {
			if n, e := strconv.ParseInt(t.HolderID, 10, 64); e != nil || n < 1 {
				return s, fmt.Errorf("持卡人 ID 无效")
			}
		}
	}
	return s, nil
}

func (t Template) OrderedProductIDs() []string {
	if t.ProductIDs != nil {
		return append([]string{}, t.ProductIDs...)
	}
	if t.ProductID != "" {
		return []string{t.ProductID}
	}
	return []string{}
}

func (t Template) SelectProduct(products []Product) (Product, error) {
	amount, err := Cents(t.Amount)
	if err != nil {
		return Product{}, err
	}
	for _, id := range t.OrderedProductIDs() {
		for _, p := range products {
			minimum, err := Cents(p.Minimum)
			if p.ID == id && err == nil && amount >= minimum {
				return p, nil
			}
		}
	}
	return Product{}, fmt.Errorf("模板所选产品均已下架、不可用或不满足最低充值金额，请调整产品或金额")
}

func CardID(serial string) string {
	h := sha256.Sum256([]byte(serial))
	return fmt.Sprintf("nex-%x", h[:16])
}

// Zero values migrate old installations without changing their saved cadence.
func (s Settings) WithSyncDefaults() Settings {
	legacy := max(s.PageRefreshSeconds, 60)
	if s.BalanceSyncSeconds == 0 {
		s.BalanceSyncSeconds = legacy
	}
	if s.CardSyncSeconds == 0 {
		s.CardSyncSeconds = legacy
	}
	if s.TransactionSyncSeconds == 0 {
		s.TransactionSyncSeconds = legacy
	}
	if s.AccountSyncSeconds == 0 {
		s.AccountSyncSeconds = max(1800, legacy)
	}
	if s.ProductSyncSeconds == 0 {
		s.ProductSyncSeconds = 1800
	}
	return s
}
