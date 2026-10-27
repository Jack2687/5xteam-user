package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
)

func TestProListSnapshotPreservesDisplayAndCredentials(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, email := range []string{"one@example.test", "two@example.test"} {
		_, err = st.SaveMailAccountToPro(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, GptPassword: "fixture-password", AccessToken: "fixture-access-token"})
		if err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: st}
	profiles, _, _, err := st.ProAccountsPageContext(t.Context(), "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := []model.MailAccountProfile{}
	for _, v := range profiles {
		items = append(items, v.MailAccountProfile)
	}
	want, err := s.proAccountListWithCardsContext(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		want[i].StateEnteredAt = profiles[i].StateEnteredAt
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var expected []proAccountListItem
	if err = json.Unmarshal(encoded, &expected); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/pro-accounts?page=1&page_size=50", nil)
	w := httptest.NewRecorder()
	s.listProAccounts(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var got struct {
		Data struct {
			Items []proAccountListItem `json:"items"`
			Total int                  `json:"total"`
		}
	}
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Total != 2 || !reflect.DeepEqual(expected, got.Data.Items) {
		t.Fatal("snapshot changed list fields")
	}
	for _, secret := range []string{"fixture-password", "fixture-access-token"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("credential leaked in list")
		}
	}
	for _, v := range got.Data.Items {
		if !v.GptPasswordPresent || !v.AccessTokenPresent {
			t.Fatal("credential presence projection lost")
		}
	}
}
