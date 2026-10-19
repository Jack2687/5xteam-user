package httpapi

import (
	"chatgpt-space-merge/internal/vm2api"
	"net/http"
)

func (s *Server) getVMGroups(w http.ResponseWriter, r *http.Request) {
	v, key, ok := s.vmSelectionConnection(w, r)
	if !ok {
		return
	}
	groups, err := vm2api.New(v, key).Groups(r.Context())
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, groups, "")
}

func (s *Server) getProVMGroups(w http.ResponseWriter, r *http.Request) {
	v, key, ok := s.proVMConnection(w, r)
	if !ok {
		return
	}
	groups, err := vm2api.New(v, key).Groups(r.Context())
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, groups, "")
}
