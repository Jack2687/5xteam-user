package httpapi

import (
	"chatgpt-space-merge/internal/store"
	"net/http"
	"sync"
)

func (s *Server) getEJHOccupancyOrder(w http.ResponseWriter, r *http.Request) {
	order, _, err := s.store.GPTPayOrder(r.PathValue("id"))
	if err != nil {
		writeAPI(w, 404, nil, "原开通订单不存在")
		return
	}
	writeAPI(w, 200, order, "") // Never return the encrypted request snapshot.
}

func (s *Server) resolveEJHOccupancy(w http.ResponseWriter, r *http.Request) {
	s.resolveCardOccupancy(w, r, false)
}

func (s *Server) resolveNexovccOccupancy(w http.ResponseWriter, r *http.Request) {
	s.resolveCardOccupancy(w, r, true)
}

func (s *Server) resolveCardOccupancy(w http.ResponseWriter, r *http.Request, nexovcc bool) {
	var in store.EJHOccupancyResolutionInput
	if decodeJSON(w, r, &in, 8<<10) != nil {
		return
	}
	order, _, err := s.store.GPTPayOrder(r.PathValue("id"))
	if err != nil {
		writeAPI(w, 404, nil, "原开通订单不存在")
		return
	}
	v, _ := s.proLocks.LoadOrStore("account:"+order.Email, &sync.Mutex{})
	lock := v.(*sync.Mutex)
	if !lock.TryLock() {
		writeAPI(w, 409, nil, "原订单正在处理或查询，请稍后重试")
		return
	}
	defer lock.Unlock()
	if s.proAutomationRunning(order.Email) {
		writeAPI(w, 409, nil, "原账号仍在执行自动流程，请先停止并核实结果")
		return
	}
	if nexovcc {
		err = s.store.ResolveNexovccOccupancy(order.ID, in)
	} else {
		err = s.store.ResolveEJHOccupancy(order.ID, in)
	}
	if err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"resolved": true, "released": in.Outcome == "not_activated"}, "")
}
