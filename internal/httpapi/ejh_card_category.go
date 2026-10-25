package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"chatgpt-space-merge/internal/store"
)

func (s *Server) ejhCardCategory(w http.ResponseWriter, r *http.Request) {
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
	s.ejh.mu.Lock()
	defer s.ejh.mu.Unlock()
	card, err := s.store.SetEJHCardNormalPending(strings.TrimSpace(r.PathValue("id")), input.Category == "normal_pending")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeAPI(w, 404, nil, "EJH 卡片不存在")
	case errors.Is(err, store.ErrEJHClosedCategory):
		writeAPI(w, 409, nil, err.Error())
	case err != nil:
		writeAPI(w, 500, nil, "保存卡片分类失败")
	default:
		// Classification responses never expose card secrets.
		card.Remote.Number, card.Remote.CVV = maskEJH(card.Remote.Number), ""
		writeAPI(w, 200, card, "")
	}
}
