package httpapi

import "net/http"

func (s *Server) linkEJHCharge(w http.ResponseWriter, r *http.Request) {
	writeAPI(w, http.StatusGone, nil, "已改为自动识别付款，请刷新页面")
}
