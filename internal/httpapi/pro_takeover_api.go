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
		groups = p.Sub2.GroupIDs
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
	targets, base := p.Sub2.GroupIDs, peer.PushGroups
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
			if input.Role == "receiver" {
				p, _, _, err := s.store.ProSettings()
				if err != nil {
					writeAPI(w, 500, nil, "Pro 推送设置读取失败")
					return
				}
				input.Groups = slices.Clone(p.Sub2.GroupIDs)
			}
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
			if input.Password != "" {
				secret = input.Password
			} else if old.PeerURL != input.PeerURL || old.Username != input.Username {
				writeAPI(w, 400, nil, "修改连接地址或用户名时请重新填写密码")
				return
			}
			var peer takeoverPeerInfo
			if !save || input.Enabled {
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
			if input.Enabled && input.Role == "receiver" {
				rc, re := s.store.ProRestockConfig()
				p, _, _, pe := s.store.ProSettings()
				if re != nil || pe != nil || !slices.Contains(input.Groups, rc.GroupID) {
					writeAPI(w, 400, nil, "接管分组必须包含本项目监控分组")
					return
				}
				for _, g := range input.Groups {
					if !slices.Contains(p.Sub2.GroupIDs, g) {
						writeAPI(w, 400, nil, "接管分组必须属于本项目 Pro 推送分组")
						return
					}
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
}
