package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Pro rows carry the full workflow and financial history. Compress transport,
// preserving the API shape and no-store policy rather than dropping details.
func writeProAccountList(w http.ResponseWriter, r *http.Request, data any) {
	body, err := json.Marshal(response{OK: true, Data: data})
	if err != nil {
		writeAPI(w, 500, nil, "读取 Pro 列表失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept-Encoding")
	if len(body) >= 2048 && acceptsListGzip(r.Header.Get("Accept-Encoding")) {
		var compressed bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		_, _ = writer.Write(body)
		_ = writer.Close()
		if compressed.Len() < len(body) {
			body = compressed.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func acceptsListGzip(header string) bool {
	for _, entry := range strings.Split(header, ",") {
		parts := strings.Split(entry, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			continue
		}
		for _, option := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(option), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				return err == nil && q > 0 && q <= 1
			}
		}
		return true
	}
	return false
}
