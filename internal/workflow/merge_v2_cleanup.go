package workflow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Read every page without trusting a fuzzy search or a successful DELETE alone.
func (c *Client) ConfirmMergeV2Removed(ctx context.Context, token, team, email, userID string) error {
	for _, kind := range []string{"users", "invites"} {
		complete := false
		offset := 0
		for page := 0; page < 100; page++ {
			v, err := c.getJSON(ctx, fmt.Sprintf("/accounts/%s/%s?offset=%d&limit=100", url.PathEscape(team), kind, offset), token, team)
			if err != nil {
				return err
			}
			rows := jsonItems(v)
			var raw []any
			for _, key := range []string{"items", "users", "members", "invites", "account_invites"} {
				if items, ok := v[key].([]any); ok {
					raw = items
					break
				}
			}
			if raw != nil && len(raw) != len(rows) {
				return errors.New("移出复查列表存在无效记录")
			}
			if rows == nil {
				return errors.New("移出复查响应缺少成员或邀请列表")
			}
			for _, row := range rows {
				id, _ := row["id"].(string)
				e, _ := row["email"].(string)
				if e == "" {
					e, _ = row["email_address"].(string)
				}
				if strings.EqualFold(e, email) || (kind == "users" && id == userID) {
					return errors.New("本单子号仍在原团队成员或邀请列表中")
				}
				if e == "" {
					return errors.New("移出复查身份数据不完整")
				}
			}
			if total, exists := v["total"]; exists {
				n, ok := total.(float64)
				if !ok || n < float64(offset+len(rows)) || n != float64(int(n)) {
					return errors.New("移出复查总数无效")
				}
			}
			more, hasMore := v["has_more"].(bool)
			if _, exists := v["has_more"]; exists && !hasMore {
				return errors.New("移出复查分页标识无效")
			}
			if n, ok := v["total"].(float64); ok {
				if hasMore && !more && float64(offset+len(rows)) < n {
					return errors.New("移出复查分页标识冲突")
				}
				if more && float64(offset+len(rows)) >= n {
					return errors.New("移出复查分页标识冲突")
				}
				if float64(offset+len(rows)) >= n {
					complete = true
					break
				}
				if len(rows) == 0 {
					return errors.New("移出复查分页数据不完整")
				}
			} else if (hasMore && !more) || !more && len(rows) < 100 {
				complete = true
				break
			}
			if len(rows) == 0 {
				return errors.New("移出复查分页未结束但没有记录")
			}
			offset += len(rows)
		}
		if !complete {
			return errors.New("移出复查超过分页上限")
		}
	}
	return nil
}
