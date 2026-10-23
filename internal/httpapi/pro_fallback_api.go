package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/sub2"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type fallbackInput struct {
	restock.FallbackConfig
	Password string `json:"password"`
}

func (s *Server) registerProFallback(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pro-restock/fallback", s.getProFallback)
	mux.HandleFunc("PUT /api/pro-restock/fallback", s.saveProFallback)
	mux.HandleFunc("POST /api/pro-restock/fallback/groups", s.fallbackGroups)
	mux.HandleFunc("POST /api/pro-restock/fallback/accounts", s.fallbackAccounts)
	mux.HandleFunc("GET /api/pro-restock/fallback/monitor-groups", s.fallbackMonitorGroups)
	mux.HandleFunc("POST /api/pro-restock/fallback/retry", func(w http.ResponseWriter, r *http.Request) {
		if err := s.tickProFallback(r.Context(), true); err != nil {
			writeAPI(w, 409, nil, err.Error())
			return
		}
		s.getProFallback(w, r)
	})
}
func (s *Server) getProFallback(w http.ResponseWriter, r *http.Request) {
	f, target, err := s.store.ProFallbackConfig()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	c, err := s.store.ProRestockConfig()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	bindings, err := s.store.ProFallbackBindings()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	now := s.fallbackNow()
	s.proFallback.mu.Lock()
	checked, lastError := s.proFallback.lastCheck, s.proFallback.lastError
	saved := append([]fallbackRuleState(nil), s.proFallback.rules...)
	s.proFallback.mu.Unlock()
	p, password, _, _ := s.store.ProSettings()
	rows := fallbackRowsForDisplay(f, c, p, password, saved, now)
	accounts := []map[string]any{}
	if f.AccountNames == nil {
		f.AccountNames = map[int64]string{}
	}
	for _, b := range bindings {
		if b.ConnectionID == target && b.Name != "" {
			f.AccountNames[b.AccountID] = b.Name
		}
		desired, hold, _, _ := fallbackAccountPlan(f, rows, b.AccountID, nil, now)
		accounts = append(accounts, map[string]any{"id": b.AccountID, "name": b.Name, "group_id": b.GroupID, "desired": b.Desired, "planned": desired && b.ConnectionID == target, "hold": hold, "observed": b.Observed, "error": b.Error, "checked_at": b.CheckedAt, "old_connection": b.ConnectionID != target})
	}
	observation := restock.FallbackObservation{}
	if len(rows) > 0 {
		observation = rows[0].Monitor
	}
	f.Rules = f.EffectiveRules(c.GroupID)
	writeAPI(w, 200, map[string]any{"config": f, "plan": fallbackSummary(f, rows), "monitor": observation, "rules": rows, "accounts": accounts, "server_time": now, "checked_at": checked, "error": lastError}, "")
}

func (s *Server) fallbackMonitorGroups(w http.ResponseWriter, r *http.Request) {
	p, password, _, err := s.store.ProSettings()
	if err != nil || p.Provider != "sub2" || p.Sub2.URL == "" || p.Sub2.Email == "" || password == "" {
		writeAPI(w, 400, nil, "请先配置 Pro 推送的主 Sub2 连接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	groups, err := s.fallbackClient("primary").Groups(ctx, p.Sub2, password)
	if err != nil {
		writeAPI(w, 400, nil, fallbackError("读取监控分组", err))
		return
	}
	writeAPI(w, 200, groups, "")
}
func (s *Server) readFallbackInput(w http.ResponseWriter, r *http.Request) (fallbackInput, model.DownstreamTarget, bool) {
	v := fallbackInput{FallbackConfig: restock.DefaultFallbackConfig()}
	var target model.DownstreamTarget
	if decodeJSON(w, r, &v, 64<<10) != nil {
		return v, target, false
	}
	v.URL = strings.TrimRight(strings.TrimSpace(v.URL), "/")
	v.Email = strings.TrimSpace(v.Email)
	v.NormalizeGroups()
	if err := v.Validate(); err != nil {
		writeAPI(w, 400, nil, err.Error())
		return v, target, false
	}
	old, id, err := s.store.ProFallbackConfig()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return v, target, false
	}
	secret := strings.TrimSpace(v.Password)
	if secret == "" && id != "" && v.URL == old.URL && v.Email == old.Email {
		saved, e := s.store.DownstreamTarget(id)
		if e != nil {
			writeAPI(w, 500, nil, "无法读取已保存兜底连接")
			return v, target, false
		}
		secret = saved.Secret
	}
	if (v.URL != "" || v.Enabled) && secret == "" {
		writeAPI(w, 400, nil, "请输入兜底 Sub2 管理员密码；更换地址或邮箱必须重新填写")
		return v, target, false
	}
	target = model.DownstreamTarget{Scope: "pro-fallback", Provider: "sub2", Sub2: model.Sub2Settings{URL: v.URL, Email: v.Email}, Secret: secret}
	return v, target, true
}
func fallbackList(ctx context.Context, client *sub2.Client, v fallbackInput, t model.DownstreamTarget) ([]sub2.FallbackAccount, error) {
	groups, err := client.Groups(ctx, t.Sub2, t.Secret)
	if err != nil {
		return nil, errors.New(fallbackError("读取兜底分组", err))
	}
	selected := v.SelectedGroupIDs()
	if len(selected) == 0 {
		return nil, errors.New("请选择兜底分组")
	}
	found := map[int64]bool{}
	for _, g := range groups {
		found[g.ID] = true
	}
	for _, id := range selected {
		if !found[id] {
			return nil, fmt.Errorf("兜底分组 #%d 已不存在或不是 OpenAI 分组，请取消选择该分组后保存", id)
		}
	}
	accounts := []sub2.FallbackAccount{}
	seen := map[int64]bool{}
	for _, id := range selected {
		items, err := client.FallbackAccounts(ctx, t.Sub2, t.Secret, id)
		if err != nil {
			return nil, errors.New(fallbackError("读取兜底账号", err))
		}
		for _, a := range items {
			if !seen[a.ID] {
				accounts = append(accounts, a)
				seen[a.ID] = true
			}
		}
	}
	return accounts, nil
}
func (s *Server) fallbackGroups(w http.ResponseWriter, r *http.Request) {
	_, t, ok := s.readFallbackInput(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	groups, err := sub2.New().Groups(ctx, t.Sub2, t.Secret)
	if err != nil {
		writeAPI(w, 400, nil, fallbackError("读取兜底分组", err))
		return
	}
	writeAPI(w, 200, groups, "")
}
func (s *Server) fallbackAccounts(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.readFallbackInput(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	client := sub2.New()
	groups, err := client.Groups(ctx, t.Sub2, t.Secret)
	if err != nil {
		writeAPI(w, 400, nil, fallbackError("读取兜底分组", err))
		return
	}
	found := map[int64]bool{}
	for _, g := range groups {
		found[g.ID] = true
	}
	accounts := []sub2.FallbackAccount{}
	missing, failed := []int64{}, []int64{}
	warnings := []string{}
	seen := map[int64]bool{}
	for _, id := range v.SelectedGroupIDs() {
		if !found[id] {
			missing = append(missing, id)
			warnings = append(warnings, fmt.Sprintf("兜底分组 #%d 已不存在或不是 OpenAI 分组，请取消选择该分组", id))
			continue
		}
		items, e := client.FallbackAccounts(ctx, t.Sub2, t.Secret, id)
		if e != nil {
			failed = append(failed, id)
			warnings = append(warnings, fmt.Sprintf("分组 #%d：%s", id, fallbackError("读取兜底账号", e)))
			continue
		}
		for _, a := range items {
			if !seen[a.ID] {
				accounts = append(accounts, a)
				seen[a.ID] = true
			}
		}
	}
	writeAPI(w, 200, map[string]any{"accounts": accounts, "missing_group_ids": missing, "failed_group_ids": failed, "warnings": warnings, "complete": len(failed) == 0}, "")
}
func (s *Server) saveProFallback(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.readFallbackInput(w, r)
	if !ok {
		return
	}
	// Keep only names verified by this server for the same connection. Names are
	// display snapshots; they never grant membership or scheduling permission.
	names := map[int64]string{}
	old, oldTarget, err := s.store.ProFallbackConfig()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	if old.URL == v.URL && old.Email == v.Email {
		for id, name := range old.AccountNames {
			names[id] = name
		}
		bindings, e := s.store.ProFallbackBindings()
		if e != nil {
			writeAPI(w, 500, nil, e.Error())
			return
		}
		for _, b := range bindings {
			if b.ConnectionID == oldTarget && b.Name != "" {
				names[b.AccountID] = b.Name
			}
		}
	}
	if v.Enabled && len(v.AccountIDs) > 0 {
		c, e := s.store.ProRestockConfig()
		p, password, _, pe := s.store.ProSettings()
		if e != nil || pe != nil || p.Provider != "sub2" || p.Sub2.URL == "" || p.Sub2.Email == "" || password == "" {
			writeAPI(w, 400, nil, "请先配置 Pro 推送的主 Sub2 连接")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		monitors, err := s.fallbackClient("primary").Groups(ctx, p.Sub2, password)
		if err != nil {
			writeAPI(w, 400, nil, fallbackError("读取监控分组", err))
			return
		}
		monitorIDs := map[int64]bool{}
		for _, g := range monitors {
			monitorIDs[g.ID] = true
		}
		accounts, err := fallbackList(ctx, sub2.New(), v, t)
		if err != nil {
			writeAPI(w, 400, nil, err.Error())
			return
		}
		found := map[int64]sub2.FallbackAccount{}
		for _, a := range accounts {
			found[a.ID] = a
			names[a.ID] = a.Name
		}
		for _, rule := range v.EffectiveRules(c.GroupID) {
			if !rule.Enabled {
				continue
			}
			if !monitorIDs[rule.MonitorGroupID] {
				writeAPI(w, 400, nil, "所选监控分组不存在或不是 OpenAI 分组，请重新读取")
				return
			}
			for _, id := range rule.AccountIDs {
				a, ok := found[id]
				member := false
				for _, group := range rule.GroupIDs {
					if a.InGroup(group) {
						member = true
					}
				}
				if !ok || !member {
					writeAPI(w, 400, nil, fmt.Sprintf("账号 %s #%d 已删除、已移出本规则的兜底分组或类型不支持，请刷新账号列表并取消选择后保存", names[id], id))
					return
				}
				if demandServerKey(p.Sub2.URL) == demandServerKey(t.Sub2.URL) && a.InGroup(rule.MonitorGroupID) {
					writeAPI(w, 400, nil, "兜底账号不能同时属于本条规则在同一 Sub2 的监控分组，否则会反复开关调度")
					return
				}
			}
		}
	}
	v.AccountNames = map[int64]string{}
	for _, rule := range v.EffectiveRules(0) {
		for _, id := range rule.AccountIDs {
			if name := names[id]; name != "" {
				v.AccountNames[id] = name
			}
		}
	}
	if !s.proFallback.tick.TryLock() {
		writeAPI(w, 409, nil, "兜底正在核验，请稍后保存")
		return
	}
	defer s.proFallback.tick.Unlock()
	id := ""
	if t.Sub2.URL != "" {
		id, err = s.store.SaveDownstreamTarget(t)
		if err != nil {
			writeAPI(w, 500, nil, err.Error())
			return
		}
	}
	f, err := s.store.SaveProFallbackConfig(v.FallbackConfig, id)
	if err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	s.proFallback.next = time.Time{}
	writeAPI(w, 200, f, "")
}
