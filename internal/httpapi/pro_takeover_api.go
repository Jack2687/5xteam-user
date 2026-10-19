package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/sub2"
)

type proTakeoverRuntime struct {
	mu   sync.Mutex
	next time.Time
	now  func() time.Time
}
type takeoverPeerInfo struct {
	ID           string                 `json:"id"`
	Role         string                 `json:"role"`
	Enabled      bool                   `json:"enabled"`
	Downstream   model.ProDownstreamRef `json:"downstream"`
	PushGroups   []int64                `json:"push_groups"`
	MonitorGroup int64                  `json:"monitor_group"`
	Groups       []int64                `json:"groups"`
	CardProvider string                 `json:"card_provider"`
	State        restock.TakeoverState  `json:"state"`
}
type takeoverHeartbeat struct {
	ID         string                 `json:"id"`
	Epoch      string                 `json:"epoch"`
	Willing    bool                   `json:"willing"`
	Pending    int                    `json:"pending"`
	Covered    int                    `json:"covered"`
	Released   bool                   `json:"released"`
	Downstream model.ProDownstreamRef `json:"downstream"`
	PushGroups []int64                `json:"push_groups"`
}

func (s *Server) takeoverInfo() (takeoverPeerInfo, error) {
	var v takeoverPeerInfo
	c, _, e := s.store.ProTakeoverConfig()
	if e != nil {
		return v, e
	}
	p, _, _, e := s.store.ProSettings()
	if e != nil {
		return v, e
	}
	rc, e := s.store.ProRestockConfig()
	if e != nil {
		return v, e
	}
	st, e := s.store.ProTakeoverState()
	if e != nil {
		return v, e
	}
	groups := c.Groups
	if c.Role == "receiver" {
		if st.Active() {
			groups = st.Groups
		}
	}
	return takeoverPeerInfo{ID: s.store.TakeoverInstanceID(), Role: c.Role, Enabled: c.Enabled, Downstream: model.ProDownstream(p), PushGroups: p.Sub2.GroupIDs, MonitorGroup: rc.GroupID, Groups: groups, CardProvider: p.AutoCardProvider(), State: st}, nil
}

// Dedicated authenticated endpoint, using the same Space login/password.
// No redirects: credentials must never follow a redirect to another host.
func takeoverPeerCall(ctx context.Context, c restock.TakeoverConfig, password, method string, body any, out any) error {
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.PeerURL, "/")+"/api/pro-takeover/peer", bytes.NewReader(data))
	if e != nil {
		return errors.New("对方地址无效")
	}
	req.SetBasicAuth(c.Username, password)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("对方连接失败或超时，请检查地址与网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("对方返回 HTTP %d，请检查账密、接管角色和连接配置", resp.StatusCode)
	}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); e != nil || len(envelope.Data) == 0 {
		return errors.New("对方接管接口响应无效，请确认两边已更新版本")
	}
	if envelope.Error != "" {
		return errors.New("对方接管请求被拒绝，请检查双方配置")
	}
	return json.Unmarshal(envelope.Data, out)
}
func (s *Server) validateTakeoverPeer(c restock.TakeoverConfig, p model.ProSettings, peer takeoverPeerInfo) error {
	if peer.ID == "" || peer.ID == s.store.TakeoverInstanceID() {
		return errors.New("对方不能是本项目")
	}
	if c.PeerID != "" && c.PeerID != peer.ID {
		return errors.New("对方项目身份变化，请重新核对连接")
	}
	if c.Role == peer.Role {
		return errors.New("两边应分别选择提供接管和接受接管")
	}
	if p.Provider != "sub2" || !model.ProDownstream(p).Matches(peer.Downstream) {
		return errors.New("两边必须连接同一个 Sub2 地址和登录账号")
	}
	if c.Role == "provider" && p.AutoCardProvider() != "nexovcc" {
		return errors.New("提供接管项目的 Pro 默认卡台应为环球")
	}
	if c.Role == "receiver" && p.AutoCardProvider() != "ejh" {
		return errors.New("接受接管项目的 Pro 默认卡台应为 EJH")
	}
	targets, base := c.Groups, peer.PushGroups
	if c.Role == "provider" {
		targets, base = peer.Groups, p.Sub2.GroupIDs
	}
	for _, g := range targets {
		if slices.Contains(base, g) {
			return errors.New("接管分组不能同时是提供方的常驻推送分组")
		}
	}
	return nil
}
func (s *Server) registerProTakeover(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pro-takeover", func(w http.ResponseWriter, r *http.Request) {
		c, _, e := s.store.ProTakeoverConfig()
		st, se := s.store.ProTakeoverState()
		if e != nil || se != nil {
			writeAPI(w, 500, nil, "接管配置读取失败")
			return
		}
		writeAPI(w, 200, map[string]any{"config": c, "state": st, "instance_id": s.store.TakeoverInstanceID()}, "")
	})
	mux.HandleFunc("GET /api/pro-takeover/peer", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.takeoverInfo()
		if e != nil {
			writeAPI(w, 500, nil, "接管状态读取失败")
			return
		}
		writeAPI(w, 200, v, "")
	})
	mux.HandleFunc("POST /api/pro-takeover/peer", s.acceptTakeoverHeartbeat)
	handler := func(save bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				restock.TakeoverConfig
				Password string `json:"password"`
			}
			input.TakeoverConfig = restock.DefaultTakeoverConfig()
			if decodeJSON(w, r, &input, 32<<10) != nil {
				return
			}
			input.PeerURL = strings.TrimRight(strings.TrimSpace(input.PeerURL), "/")
			input.Username = strings.TrimSpace(input.Username)
			// Connection test also works before a peer identity has been pinned.
			test := input.TakeoverConfig
			test.Enabled = false
			if e := test.Validate(); e != nil {
				writeAPI(w, 400, nil, e.Error())
				return
			}
			old, secret, e := s.store.ProTakeoverConfig()
			if e != nil {
				writeAPI(w, 500, nil, e.Error())
				return
			}
			if save && input.Version != old.Version {
				writeAPI(w, 409, nil, "接管配置已变化，请刷新重试")
				return
			}
			if input.Password != "" {
				secret = input.Password
			} else if old.PeerURL != input.PeerURL || old.Username != input.Username {
				writeAPI(w, 400, nil, "修改连接地址或用户名时请重新填写密码")
				return
			}
			var peer takeoverPeerInfo
			// Saving local configuration never depends on the other project being
			// configured or reachable. Only the explicit enable endpoint starts it.
			connectionChanged := old.PeerURL != input.PeerURL || old.Username != input.Username || old.Role != input.Role
			input.PeerID = old.PeerID
			if old.PeerURL != input.PeerURL {
				input.PeerID = ""
			}
			if !save {
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				defer cancel()
				if e = takeoverPeerCall(ctx, input.TakeoverConfig, secret, http.MethodGet, nil, &peer); e == nil {
					p, _, _, pe := s.store.ProSettings()
					e = pe
					if e == nil {
						e = s.validateTakeoverPeer(input.TakeoverConfig, p, peer)
					}
				}
				if e != nil {
					writeAPI(w, 400, nil, e.Error())
					return
				}
				input.PeerID = peer.ID
			}
			if !save {
				writeAPI(w, 200, peer, "")
				return
			}
			s.proTakeover.mu.Lock()
			defer s.proTakeover.mu.Unlock()
			if old.Enabled && connectionChanged {
				writeAPI(w, 409, nil, "请先停用接管，再修改角色或连接信息")
				return
			}
			input.Enabled = old.Enabled
			if input.Enabled {
				if e = s.validateTakeoverSelection(input.TakeoverConfig); e != nil {
					writeAPI(w, 400, nil, e.Error())
					return
				}
			}
			c, e := s.store.SaveProTakeoverConfig(input.TakeoverConfig, input.Password)
			if e != nil {
				writeAPI(w, 409, nil, e.Error())
				return
			}
			s.proTakeover.next = time.Time{}
			writeAPI(w, 200, c, "")
		}
	}
	mux.HandleFunc("POST /api/pro-takeover/test", handler(false))
	mux.HandleFunc("PUT /api/pro-takeover/config", handler(true))
	mux.HandleFunc("POST /api/pro-takeover/enabled", s.setProTakeoverEnabled)
}

// Keep the receiver's monitored group covered while allowing any other current
// Sub2 OpenAI groups to be explicitly selected, independently of normal pushes.
func (s *Server) validateTakeoverSelection(c restock.TakeoverConfig) error {
	if c.Role != "receiver" {
		return nil
	}
	rc, e := s.store.ProRestockConfig()
	if e != nil {
		return errors.New("补货设置读取失败")
	}
	if !slices.Contains(c.Groups, rc.GroupID) {
		return errors.New("接管分组必须包含本项目监控分组")
	}
	return nil
}

func (s *Server) setProTakeoverEnabled(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
		Version int  `json:"version"`
	}
	if decodeJSON(w, r, &input, 4096) != nil {
		return
	}
	c, password, e := s.store.ProTakeoverConfig()
	if e != nil {
		writeAPI(w, 500, nil, "接管配置读取失败")
		return
	}
	if input.Version != c.Version {
		writeAPI(w, 409, nil, "接管配置已变化，请刷新重试")
		return
	}
	c.Enabled = input.Enabled
	if input.Enabled {
		if c.PeerURL == "" || c.Username == "" || password == "" {
			writeAPI(w, 400, nil, "请先保存对方地址、登录账号和密码")
			return
		}
		if e = s.validateTakeoverSelection(c); e != nil {
			writeAPI(w, 400, nil, e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		var peer takeoverPeerInfo
		if e = takeoverPeerCall(ctx, c, password, http.MethodGet, nil, &peer); e != nil {
			writeAPI(w, 400, nil, e.Error())
			return
		}
		p, secret, _, pe := s.store.ProSettings()
		if pe != nil {
			writeAPI(w, 500, nil, "Pro 推送设置读取失败")
			return
		}
		if e = s.validateTakeoverPeer(c, p, peer); e != nil {
			writeAPI(w, 400, nil, e.Error())
			return
		}
		if c.Role == "receiver" {
			groups, ge := s.sub2.Groups(ctx, p.Sub2, secret)
			if ge != nil {
				writeAPI(w, 400, nil, "读取 Sub2 分组失败，请刷新分组后重试")
				return
			}
			for _, id := range c.Groups {
				if !slices.ContainsFunc(groups, func(g sub2.Group) bool { return g.ID == id }) {
					writeAPI(w, 400, nil, fmt.Sprintf("接管分组 #%d 已不存在或不是 OpenAI 分组，请刷新分组重新选择", id))
					return
				}
			}
		}
		c.PeerID = peer.ID
	}
	// No network request is made while holding the monitor lock. The store's
	// version check prevents a late response from overwriting newer settings.
	s.proTakeover.mu.Lock()
	defer s.proTakeover.mu.Unlock()
	c, e = s.store.SaveProTakeoverConfig(c, "")
	if e != nil {
		writeAPI(w, 409, nil, e.Error())
		return
	}
	s.proTakeover.next = time.Time{}
	writeAPI(w, 200, c, "")
}
