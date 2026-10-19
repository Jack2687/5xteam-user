package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chatgpt-space-merge/internal/workflow"
)

func (s *Server) adminAccountMembers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	offset, limit := 0, s.defaultPageSize()
	for key, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || (key == "limit" && (value < 1 || value > 500)) {
				writeAPI(w, http.StatusBadRequest, nil, "分页参数无效，每页最多 500 条")
				return
			}
			*dest = value
		}
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(query) > 320 {
		writeAPI(w, http.StatusBadRequest, nil, "搜索内容过长")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	profile, credentials, err := s.currentAdminCredential(ctx, r.PathValue("id"))
	if err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	settings, err := s.settingsForAdmin(s.store.Settings(), profile)
	if err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	client, err := workflow.NewClient(settings)
	if err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	// OpenAI accepts at most 25 members per request. Assemble the requested
	// display page sequentially without changing that upstream contract.
	page := workflow.TeamMemberPage{Items: []workflow.TeamMember{}, Total: -1, Offset: offset, Limit: limit}
	for len(page.Items) < limit {
		batchSize := min(25, limit-len(page.Items))
		batch, err := client.ListTeamMembers(ctx, credentials.AccessToken, profile.TeamAccountID, query, offset+len(page.Items), batchSize)
		if err != nil {
			writeAPI(w, http.StatusBadGateway, nil, err.Error())
			return
		}
		if len(batch.Items) > batchSize {
			writeAPI(w, http.StatusBadGateway, nil, "母号成员查询返回的条数超出请求范围")
			return
		}
		page.Items = append(page.Items, batch.Items...)
		page.Total, page.HasMore = batch.Total, batch.HasMore
		if !batch.HasMore || len(batch.Items) == 0 {
			break
		}
	}
	type memberView struct {
		workflow.TeamMember
		CanKick     bool   `json:"can_kick"`
		CanLeave    bool   `json:"can_leave"`
		LeaveReason string `json:"leave_reason,omitempty"`
	}
	items := make([]memberView, 0, len(page.Items))
	for _, member := range page.Items {
		item := memberView{TeamMember: member}
		item.CanKick = member.ID != "" && member.Email != "" && !protectedAdminMember(profile, member)
		if item.CanKick {
			_, creds, err := s.store.MailAccountCredential(member.Email)
			item.CanLeave = err == nil && strings.TrimSpace(creds.AccessToken) != ""
			if !item.CanLeave {
				item.LeaveReason = "邮件管理中未保存该子号的 AT"
			}
		} else {
			item.LeaveReason = "不能操作空间所有者或身份不完整的成员"
		}
		items = append(items, item)
	}
	writeAPI(w, http.StatusOK, map[string]any{
		"items": items, "total": page.Total, "offset": page.Offset, "limit": page.Limit,
		"has_more": page.HasMore, "queried_at": time.Now().UTC(), "team_account_id": profile.TeamAccountID,
	}, "")
}
