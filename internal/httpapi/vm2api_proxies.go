package httpapi

import (
	"net/http"
	"strings"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/vm2api"
)

func (s *Server) vmSelectionConnection(w http.ResponseWriter, r *http.Request) (model.VM2APISettings, string, bool) {
	settings, key, err := s.store.VM2APISettings()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return settings, "", false
	}
	var input struct {
		VM2API *struct {
			URL string `json:"url"`
			Key string `json:"key"`
		} `json:"vm2api"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(w, r, &input, 1<<20); err != nil {
			return settings, "", false
		}
		if input.VM2API != nil {
			settings.URL = strings.TrimRight(strings.TrimSpace(input.VM2API.URL), "/")
			if value := strings.TrimSpace(input.VM2API.Key); value != "" {
				key = value
			}
		}
	}
	if err := vm2api.Validate(settings, key != ""); err != nil {
		writeAPI(w, 400, nil, err.Error())
		return settings, "", false
	}
	return settings, key, true
}

func (s *Server) getVMProxies(w http.ResponseWriter, r *http.Request) {
	settings, key, ok := s.vmSelectionConnection(w, r)
	if !ok {
		return
	}
	proxies, err := vm2api.New(settings, key).Proxies(r.Context())
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, proxies, "")
}
