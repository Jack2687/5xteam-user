package httpapi

import (
	"chatgpt-space-merge/internal/store"
	"database/sql"
	"errors"
	"net/http"
)

func (s *Server) saveCardPaymentPolicy(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			store.CardPaymentPolicy
			ExternalReclassifiedUSD string `json:"external_reclassified_usd"`
		}
		if decodeJSON(w, r, &input, 8<<10) != nil {
			return
		}
		input.Provider, input.CardID = provider, r.PathValue("id")
		policy, err := s.store.SaveCardPaymentPolicy(input.CardPaymentPolicy, input.ExternalReclassifiedUSD)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeAPI(w, 404, nil, "卡片或原转出记录不存在")
		case errors.Is(err, store.ErrCardPaymentPolicyConflict):
			writeAPI(w, 409, nil, err.Error())
		case err != nil:
			writeAPI(w, 400, nil, err.Error())
		default:
			writeAPI(w, 200, policy, "")
		}
	}
}
