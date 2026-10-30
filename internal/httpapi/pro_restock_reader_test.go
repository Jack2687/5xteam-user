package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/restock"
)

func TestRestockDisplayIndependentReaderAndCompression(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("display contacted upstream") })
	s.proAutoCancel()
	s.proAutoWG.Wait()
	d := restock.Decision{ID: "saved", At: time.Now(), Reason: strings.Repeat("retained decision evidence ", 500)}
	if err := st.SaveProRestockDecision(d); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerProRestock(mux)
	// The shared writer connection and issuer mutex are unavailable. Read APIs
	// must still serve all saved details without entering the business queue.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s.ejh.mu.Lock()
	defer s.ejh.mu.Unlock()
	s.proCardAdmissionMu.Lock()
	defer s.proCardAdmissionMu.Unlock()
	for _, path := range []string{"/api/pro-restock", "/api/pro-restock/records?page=1&page_size=10", "/api/pro-restock/records/saved/tasks", "/api/pro-restock/fallback"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %s", path, w.Body.String())
		}
		if path == "/api/pro-restock" || strings.Contains(path, "page_size") {
			if w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() > 5000 {
				t.Fatalf("uncompressed display: %s %d", path, w.Body.Len())
			}
			reader, err := gzip.NewReader(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || !strings.Contains(string(body), d.Reason) {
				t.Fatal("full evidence lost", err)
			}
		}
	}
}
