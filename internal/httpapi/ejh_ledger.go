package httpapi

import "net/http"

func (s *Server) saveEJHExternalTransfer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount *string `json:"amount"`
		Note   string  `json:"note"`
	}
	if decodeJSON(w, r, &in, 4<<10) != nil {
		return
	}
	if in.Amount == nil {
		writeAPI(w, http.StatusBadRequest, nil, "请填写累计平台外转出金额，留空按零记录")
		return
	}
	result, err := s.store.SaveEJHExternalTransfer(*in.Amount, in.Note)
	if err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	writeAPI(w, http.StatusOK, result, "")
}

func (s *Server) saveEJHRechargePeriod(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Month *string `json:"month"`
	}
	if decodeJSON(w, r, &in, 4<<10) != nil {
		return
	}
	if in.Month == nil {
		writeAPI(w, 400, nil, "请选择历史充值归属月份")
		return
	}
	out, err := s.store.SaveEJHRechargeHistoryMonth(*in.Month)
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, out, "")
}

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
