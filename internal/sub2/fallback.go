package sub2

import (
	"chatgpt-space-merge/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Only public account metadata is returned; account secrets are never decoded.
type FallbackAccount struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Platform    string  `json:"platform"`
	Type        string  `json:"type"`
	Status      string  `json:"status"`
	Schedulable *bool   `json:"schedulable"`
	GroupIDs    []int64 `json:"group_ids"`
	Groups      []Group `json:"groups,omitempty"`
}

func (a FallbackAccount) Supported() bool {
	return a.Platform == "openai" && (a.Type == "apikey" || a.Type == "oauth")
}
func (a FallbackAccount) InGroup(id int64) bool {
	for _, g := range a.GroupIDs {
		if g == id {
			return true
		}
	}
	for _, g := range a.Groups {
		if g.ID == id {
			return true
		}
	}
	return false
}

func (c *Client) FallbackAccounts(ctx context.Context, v model.Sub2Settings, password string, group int64) ([]FallbackAccount, error) {
	if group <= 0 {
		return nil, errors.New("请选择兜底分组")
	}
	accounts, err := c.MonitorAccounts(ctx, v, password, group)
	if err != nil {
		return nil, err
	}
	out := []FallbackAccount{}
	for _, a := range accounts {
		item := FallbackAccount{ID: a.ID, Name: a.Name, Platform: a.Platform, Type: a.Type, Status: a.Status, GroupIDs: a.GroupIDs}
		if item.Supported() {
			value := a.Schedulable
			item.Schedulable = &value
			out = append(out, item)
		}
	}
	return out, nil
}
func (c *Client) FallbackAccount(ctx context.Context, v model.Sub2Settings, password string, id int64) (FallbackAccount, error) {
	var a FallbackAccount
	raw, err := c.doJSON(ctx, v, password, http.MethodGet, fmt.Sprintf("/api/v1/admin/accounts/%d", id), nil, nil)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	if err == nil && (a.ID != id || a.Schedulable == nil) {
		err = errors.New("Sub2 账号响应缺少有效 ID 或调度状态")
	}
	return a, err
}

// SetFallbackScheduling deliberately does not clear cooldowns or change credentials/groups.
func (c *Client) SetFallbackScheduling(ctx context.Context, v model.Sub2Settings, password string, id int64, on bool) error {
	raw, err := c.doJSON(ctx, v, password, http.MethodPost, fmt.Sprintf("/api/v1/admin/accounts/%d/schedulable", id), map[string]bool{"schedulable": on}, nil)
	if err != nil {
		return err
	}
	var a FallbackAccount
	if json.Unmarshal(raw, &a) != nil || a.ID != id || a.Schedulable == nil || *a.Schedulable != on {
		return errors.New("Sub2 未确认目标调度状态，等待下轮核验")
	}
	return nil
}
