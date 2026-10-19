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

// QueryDailyUserCost uses the same user deduction measure as total_user_cost.
// The account stats history uses the downstream database timezone, so use the
// filtered usage endpoint with explicit Beijing boundaries instead.
func (c *Client) QueryDailyUserCost(ctx context.Context, settings model.Sub2Settings, password string, id int64, day string) (float64, error) {
	if _, err := time.Parse("2006-01-02", day); err != nil || id < 1 {
		return 0, errors.New("无效的消费日期或下游账号")
	}
	query := url.Values{"account_id": {fmt.Sprint(id)}, "start_date": {day}, "end_date": {day}, "timezone": {"Asia/Shanghai"}, "nocache": {"true"}}
	raw, err := c.doJSON(ctx, settings, password, http.MethodGet, "/api/v1/admin/usage/stats?"+query.Encode(), nil, nil)
	if err != nil {
		return 0, err
	}
	var stats struct {
		UserCost *float64 `json:"total_actual_cost"`
	}
	if err = json.Unmarshal(raw, &stats); err != nil {
		return 0, err
	}
	if stats.UserCost == nil || math.IsNaN(*stats.UserCost) || math.IsInf(*stats.UserCost, 0) || *stats.UserCost < 0 {
		return 0, errors.New("Sub2 缺少有效的每日用户扣费，不能用账号费用替代")
	}
	return *stats.UserCost, nil
}
