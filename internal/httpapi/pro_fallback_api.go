package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/sub2"
	"context"
	"errors"
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
	accounts := []map[string]any{}
	for _, b := range bindings {
		accounts = append(accounts, map[string]any{"id": b.AccountID, "name": b.Name, "group_id": b.GroupID, "desired": b.Desired, "observed": b.Observed, "error": b.Error, "checked_at": b.CheckedAt, "old_connection": b.ConnectionID != target})
	}
	now := s.fallbackNow()
	s.proFallback.mu.Lock()
	checked, lastError := s.proFallback.lastCheck, s.proFallback.lastError
	observation, source := s.proFallback.observation, s.proFallback.source
	s.proFallback.mu.Unlock()
	p, password, _, pe := s.store.ProSettings()
	if pe != nil || source != fallbackSourceKey(c, p, password) {
		observation = restock.FallbackObservation{GroupID: c.GroupID}
	}
	writeAPI(w, 200, map[string]any{"config": f, "plan": f.Plan(now, observation), "monitor": observation, "accounts": accounts, "server_time": now, "checked_at": checked, "error": lastError}, "")
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
			return nil, errors.New("所选兜底分组不存在或不是 OpenAI 分组，请重新读取分组")
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
	accounts, err := fallbackList(ctx, sub2.New(), v, t)
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, accounts, "")
}
func (s *Server) saveProFallback(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.readFallbackInput(w, r)
	if !ok {
		return
	}
	if v.Enabled {
		c, e := s.store.ProRestockConfig()
		p, password, _, pe := s.store.ProSettings()
		if e != nil || pe != nil || c.GroupID <= 0 || p.Provider != "sub2" || p.Sub2.URL == "" || p.Sub2.Email == "" || password == "" {
			writeAPI(w, 400, nil, "请先配置 Pro 推送 Sub2 连接和自动补货监控分组")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		accounts, err := fallbackList(ctx, sub2.New(), v, t)
		if err != nil {
			writeAPI(w, 400, nil, err.Error())
			return
		}
		found := map[int64]bool{}
		for _, a := range accounts {
			found[a.ID] = true
		}
		for _, id := range v.AccountIDs {
			if !found[id] {
				writeAPI(w, 400, nil, "所选账号已不在所选分组或不是 OpenAI API Key / OAuth 类型，请刷新账号列表")
				return
			}
		}
	}
	if !s.proFallback.tick.TryLock() {
		writeAPI(w, 409, nil, "兜底正在核验，请稍后保存")
		return
	}
	defer s.proFallback.tick.Unlock()
	id := ""
	var err error
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
