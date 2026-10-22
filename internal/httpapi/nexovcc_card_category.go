package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"chatgpt-space-merge/internal/store"
)

func (s *Server) nexovccCardCategory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Category string `json:"category"`
	}
	if decodeJSON(w, r, &input, 4<<10) != nil {
		return
	}
	if input.Category != "normal" && input.Category != "normal_pending" {
		writeAPI(w, 400, nil, "请选择正常或正常待定")
		return
	}
	card, err := s.store.SetNexovccCardNormalPending(strings.TrimSpace(r.PathValue("id")), input.Category == "normal_pending")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeAPI(w, 404, nil, "环球卡片不存在")
	case errors.Is(err, store.ErrNexovccClosedCategory):
		writeAPI(w, 409, nil, err.Error())
	case err != nil:
		writeAPI(w, 500, nil, "保存卡片分类失败")
	default:
		writeAPI(w, 200, card, "")
	}
}
