package httpapi

import (
	"chatgpt-space-merge/internal/store"
	"errors"
	"net/http"
)

func (s *Server) saveEJHRefundFee(w http.ResponseWriter, r *http.Request) {
	var in store.EJHRefundFee
	if decodeJSON(w, r, &in, 8<<10) != nil {
		return
	}
	in.ID = r.PathValue("id")
	row, err := s.store.SaveEJHRefundFee(in)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, store.ErrEJHRefundFeeConflict) {
			code = http.StatusConflict
		}
		writeAPI(w, code, nil, err.Error())
		return
	}
	writeAPI(w, http.StatusOK, row, "")
}
