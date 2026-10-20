package httpapi

import (
	"chatgpt-space-merge/internal/store"
	"net/http"
)

func (s *Server) nexovccTradeReview(w http.ResponseWriter, r *http.Request) {
	var in store.NexovccReviewInput
	if decodeJSON(w, r, &in, 8192) != nil {
		return
	}
	result, e := s.store.SaveNexovccTradeReview(r.PathValue("id"), in)
	if e != nil {
		writeAPI(w, 409, nil, e.Error())
		return
	}
	writeAPI(w, 200, result, "")
}
