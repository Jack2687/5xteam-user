package httpapi

import (
	"chapt-space-user/internal/model"
	"chapt-space-user/internal/restock"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) registerProRestock(mux *http.ServeMux) {
	s.registerProFallback(mux)
	mux.HandleFunc("GET /api/pro-restock/records/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.store.ProRestockDecisionTasks(r.PathValue("id"))
		if err != nil {
			writeAPI(w, 500, nil, err.Error())
			return
		}
		writeAPI(w, 200, items, "")
	})
	mux.HandleFunc("GET /api/pro-restock", s.getProRestock)
	mux.HandleFunc("PUT /api/pro-restock/config", s.saveProRestockConfig)
	mux.HandleFunc("GET /api/pro-restock/groups", s.getProRestockGroups)
	mux.HandleFunc("POST /api/pro-restock/evaluate", func(w http.ResponseWriter, r *http.Request) {
		if err := s.tickProRestock(r.Context(), true); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errProRestockBusy) {
				status = http.StatusConflict
			}
			writeAPI(w, status, nil, err.Error())
			return
		}
		s.getProRestock(w, r)
	})
	mux.HandleFunc("POST /api/pro-restock/reset-breaker", func(w http.ResponseWriter, r *http.Request) {
		s.proRestock.tick.Lock()
		defer s.proRestock.tick.Unlock()
		e := s.store.ResetProRestockBreaker()
		if e != nil {
			writeAPI(w, 500, nil, e.Error())
			return
		}
		c, _ := s.store.ProRestockConfig()
		_ = s.store.SaveProRestockDecision(restock.Decision{ID: randomRegistrationID(), At: time.Now(), Version: c.Version, Config: c, Reason: "人工重置异常熔断，原订单与在途继续保留"})
		s.getProRestock(w, r)
	})
	mux.HandleFunc("GET /api/pro-restock/records", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		v, e := s.store.ProRestockDecisions(30, max(0, page-1)*30)
		if e != nil {
			writeAPI(w, 500, nil, e.Error())
			return
		}
		writeAPI(w, 200, v, "")
	})
}
func (s *Server) getProRestock(w http.ResponseWriter, r *http.Request) {
	c, e := s.store.ProRestockConfig()
	if e != nil {
		writeAPI(w, 500, nil, e.Error())
		return
	}
	p, _, _, e := s.store.ProSettings()
	if e != nil {
		writeAPI(w, 500, nil, e.Error())
		return
	}
	tasks, e := s.store.ProRestockTasks()
	if e != nil {
		writeAPI(w, 500, nil, e.Error())
		return
	}
	limits, e := s.store.ProRestockLimits(time.Now())
	if e != nil {
		writeAPI(w, 500, nil, e.Error())
		return
	}
	s.proRestock.mu.Lock()
	d, snap := s.proRestock.decision, s.proRestock.lastSnapshot
	for _, t := range s.proRestock.tasks {
		if t.DecisionID == "" {
			tasks = append(tasks, t)
		}
	}
	s.proRestock.mu.Unlock()
	if snap.ID == "" {
		snap, _ = s.store.ProRestockSnapshot()
	}
	if d.ID == "" {
		records, _ := s.store.ProRestockDecisions(1, 0)
		if len(records) > 0 {
			d = records[0]
		}
	}
	now := time.Now()
	noRestockReason := c.NoRestockReason(now)
	writeAPI(w, 200, map[string]any{"config": c, "inherited": p, "snapshot": snap, "decision": d, "tasks": tasks, "limits": limits, "server_time": now, "no_restock_active": noRestockReason != "", "no_restock_reason": noRestockReason}, "")
}
func (s *Server) saveProRestockConfig(w http.ResponseWriter, r *http.Request) {
	c := restock.DefaultConfig()
	if decodeJSON(w, r, &c, 32<<10) != nil {
		return
	}
	if e := c.Validate(); e != nil {
		writeAPI(w, 400, nil, e.Error())
		return
	}
	if c.Enabled {
		p, password, _, e := s.store.ProSettings()
		if e == nil {
			e = validateRestockTarget(c, p)
		}
		if e == nil {
			e = s.validateProAutoPush(model.MailAccountProfile{}, p, password)
		}
		if e != nil {
			writeAPI(w, 400, nil, e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		snap, e := s.collectProRestock(ctx, c, p, password)
		if e != nil || !snap.Valid {
			message := snap.Error
			if e != nil {
				message = e.Error()
			}
			writeAPI(w, 400, nil, message)
			return
		}
		if c.Mode == "auto" && !snap.LoadValid {
			writeAPI(w, 400, nil, snap.Error)
			return
		}
	}
	s.proRestock.tick.Lock()
	defer s.proRestock.tick.Unlock()
	if !s.proFallback.tick.TryLock() {
		writeAPI(w, 409, nil, "兜底正在核验，请稍后保存补货配置")
		return
	}
	defer s.proFallback.tick.Unlock()
	c, e := s.store.SaveProRestockConfig(c)
	if e != nil {
		writeAPI(w, 409, nil, e.Error())
		return
	}
	_ = s.store.SaveProRestockDecision(restock.Decision{ID: randomRegistrationID(), At: time.Now(), Version: c.Version, Config: c, Reason: "配置已保存：" + c.Mode})
	s.proFallback.next = time.Time{}
	s.proRestock.next = time.Time{}
	writeAPI(w, 200, c, "")
}
func (s *Server) getProRestockGroups(w http.ResponseWriter, r *http.Request) {
	p, password, _, e := s.store.ProSettings()
	if e != nil || p.Provider != "sub2" {
		writeAPI(w, 400, nil, "请先在 Pro 推送设置选择 Sub2")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	groups, e := s.sub2.Groups(ctx, p.Sub2, password)
	if e != nil {
		writeAPI(w, 400, nil, e.Error())
		return
	}
	writeAPI(w, 200, groups, "")
}
