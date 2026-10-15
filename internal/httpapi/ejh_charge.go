package httpapi

import "net/http"

func (s *Server) linkEJHCharge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TradeID  string `json:"trade_id"`
		Evidence string `json:"evidence"`
	}
	if decodeJSON(w, r, &in, 8<<10) != nil {
		return
	}
	s.ejh.mu.Lock()
	defer s.ejh.mu.Unlock()
	if err := s.store.EJHLinkCharge(r.PathValue("id"), in.TradeID, in.Evidence); err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"saved": true}, "")
}
