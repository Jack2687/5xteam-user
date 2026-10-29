package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/ejh"
)

func TestEJHDashboardAndSettingsUseIndependentReadOnlyConnection(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("dashboard contacted supplier") })
	card := ejh.Card{OrderNo: "dashboard-saved", Number: "5555555555554444", CVV: "919", Validity: "1299", Status: "1"}
	if _, err := st.ImportEJHCard(card, true); err != nil {
		t.Fatal(err)
	}
	// Keep the persisted data but make the shared application connection
	// unavailable. Neither display handler may depend on it or the issuer lock.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s.ejh.mu.Lock()
	defer s.ejh.mu.Unlock()
	for _, name := range []string{"settings", "dashboard"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/ejh/"+name, nil)
		if name == "settings" {
			s.getEJHSettings(w, r)
		} else {
			s.ejhDashboard(w, r)
		}
		if w.Code != 200 || strings.Contains(w.Body.String(), card.Number) || strings.Contains(w.Body.String(), `"cvvPassword":"919"`) {
			t.Fatal(name, w.Code, "read failed or leaked card secret")
		}
		if name == "dashboard" && !strings.Contains(w.Body.String(), card.OrderNo) {
			t.Fatal("saved card was not returned")
		}
	}
}
