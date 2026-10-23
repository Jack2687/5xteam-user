package httpapi

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestProListCompressionPreservesResponse(t *testing.T) {
	data := map[string]any{"items": []any{map[string]any{"email": "fixture@example.test", "workflow": strings.Repeat("complete history ", 800)}}, "summary": map[string]int{"all": 140, "merged": 87, "deleted": 26}, "total": 140}
	plain := httptest.NewRecorder()
	writeAPI(plain, 200, data, "")
	var expected any
	if err := json.Unmarshal(plain.Body.Bytes(), &expected); err != nil {
		t.Fatal(err)
	}
	for _, encoding := range []string{"", "gzip", "br, gzip;q=0.8", "gzip;q=0", "gzip;q=0.0", "gzip;q=invalid"} {
		r := httptest.NewRequest("GET", "/api/pro-accounts?page=1", nil)
		r.Header.Set("Accept-Encoding", encoding)
		w := httptest.NewRecorder()
		writeProAccountList(w, r, data)
		compressed := encoding == "gzip" || encoding == "br, gzip;q=0.8"
		if (w.Header().Get("Content-Encoding") == "gzip") != compressed || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatal("headers", encoding, w.Header())
		}
		body := w.Body.Bytes()
		if compressed {
			if len(body)*3 >= plain.Body.Len() {
				t.Fatal("response was not meaningfully compressed")
			}
			reader, err := gzip.NewReader(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			reader.Close()
		}
		var actual any
		if err := json.Unmarshal(body, &actual); err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatal("changed data", err)
		}
	}
}

func TestProListCanceledRequest(t *testing.T) {
	f := newProExecutionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := f.s.store.ProAccountsPageContext(ctx, "", "", 10, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.s.proAccountListWithCardsContext(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
