package sub2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"chatgpt-space-merge/internal/model"
)

// Window cost and total_account_cost both use Sub2's account billing basis.
// Neither user deductions nor standard/list prices are interchangeable with it.
type MoneyWindow struct {
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
	Stats       *struct {
		Cost *float64 `json:"cost"`
	} `json:"window_stats"`
}
type MoneyUsage struct {
	UpdatedAt *time.Time   `json:"updated_at"`
	FiveHour  *MoneyWindow `json:"five_hour"`
	SevenDay  *MoneyWindow `json:"seven_day"`
	Day       string       `json:"-"`
	DailyCost float64      `json:"-"`
}

func (c *Client) MonitorMoney(ctx context.Context, settings model.Sub2Settings, password string, id int64) (MoneyUsage, error) {
	var out MoneyUsage
	if id < 1 {
		return out, errors.New("Sub2 账号 ID 无效")
	}
	// source=passive is Anthropic-only. OpenAI uses the regular usage reader;
	// never request a forced probe/reset or use the active-sampling endpoints.
	raw, err := c.doJSON(ctx, settings, password, http.MethodGet, fmt.Sprintf("/api/v1/admin/accounts/%d/usage", id), nil, nil)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	out.Day = time.Now().UTC().Format("2006-01-02")
	query := url.Values{"account_id": {fmt.Sprint(id)}, "start_date": {out.Day}, "end_date": {out.Day}, "timezone": {"UTC"}, "nocache": {"true"}}
	raw, err = c.doJSON(ctx, settings, password, http.MethodGet, "/api/v1/admin/usage/stats?"+query.Encode(), nil, nil)
	if err != nil {
		return out, err
	}
	var stats struct {
		Cost *float64 `json:"total_account_cost"`
	}
	if err = json.Unmarshal(raw, &stats); err != nil {
		return out, err
	}
	if stats.Cost == nil || math.IsNaN(*stats.Cost) || math.IsInf(*stats.Cost, 0) || *stats.Cost < 0 {
		return out, errors.New("Sub2 缺少有效账号费用，不能用用户扣费替代")
	}
	if out.Day != time.Now().UTC().Format("2006-01-02") {
		return out, errors.New("跨日采样，等待下一轮")
	}
	out.DailyCost = *stats.Cost
	return out, nil
}
