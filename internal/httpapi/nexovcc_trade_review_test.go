package httpapi

import (
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNexovccTradeReviewAPI(t *testing.T) {
	st, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	s := &Server{store: st, sessions: map[string]time.Time{"review": time.Now().Add(time.Hour)}}
	f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{{ID: "unverified", Serial: "card", Currency: "USD", Amount: "2.30", Status: "OTHER", At: time.Now()}}}
	if e = st.SaveNexovccFinance(f); e != nil {
		t.Fatal(e)
	}
	g := nexovcc.Groups(f)[0]
	body, _ := json.Marshal(store.NexovccReviewInput{Kind: "other_fee", Amount: "2.30", Reason: "核实为额外卡片费用", Fingerprint: nexovcc.ReviewFingerprint(f, g)})
	mux := http.NewServeMux()
	s.nexovccRoutes(mux)
	handler := s.authMiddleware(mux)
	for _, tc := range []struct {
		body   string
		auth   bool
		origin string
		code   int
	}{{string(body), false, "", 401}, {string(body), true, "https://other.example", 403}, {`{}`, true, "", 409}, {string(body), true, "", 200}, {string(body), true, "", 409}} {
		r := httptest.NewRequest("PUT", "/api/nexovcc/trade-reviews/"+g.ID, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", tc.origin)
		if tc.auth {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "review"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	groups, e := st.NexovccTradeGroups(f)
	if e != nil {
		t.Fatal(e)
	}
	if nexovccRecycleCandidate(nexovcc.Card{ID: "c", Serial: "card", Status: "1", Imported: true}, nil, groups) {
		t.Fatal("expense classification bypassed card refund guard")
	}
}
