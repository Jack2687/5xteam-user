package httpapi

import "net/http"

func (s *Server) saveEJHRechargeLedger(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount *string `json:"amount"`
	}
	if decodeJSON(w, r, &in, 4<<10) != nil {
		return
	}
	if in.Amount == nil {
		writeAPI(w, http.StatusBadRequest, nil, "请填写充值总账金额，留空可清除")
		return
	}
	result, err := s.store.SaveEJHRechargeLedger(*in.Amount)
	if err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	writeAPI(w, http.StatusOK, result, "")
}
