package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
)

func (s *Server) deleteEJHUnopenedCases(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if decodeJSON(w, r, &in, 8<<10) != nil {
		return
	}
	if !in.Confirm {
		writeAPI(w, http.StatusBadRequest, nil, "请确认删除未开通的账号回款记录")
		return
	}
	ids, err := s.store.DeleteEJHUnopenedCases(r.PathValue("id"))
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		writeAPI(w, code, nil, err.Error())
		return
	}
	writeAPI(w, http.StatusOK, map[string]any{"deleted": len(ids), "deleted_ids": ids}, "")
}
