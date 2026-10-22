package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
)

func TestMailSIWCSaveViewExportPreservesOrdinaryCredentials(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	email := "siwc@example.com"
	_, err = st.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, AccessToken: "codex-at", RefreshToken: "codex-rt", GptPassword: "gpt-password", TotpSecret: "totp-secret"})
	if err != nil {
		t.Fatal(err)
	}
	before, original, _ := st.MailAccountCredential(email)
	tokens := model.SIWCCredentials{Email: email, AuthMode: "siwc", AccessToken: "siwc-at", RefreshToken: "siwc-rt", IDToken: "siwc-id", ClientID: "oaiapp_fixture", ExtAgentHostID: st.MailSIWCHostID(email, original), Subject: "subject", ExpiresAt: time.Now().Add(time.Hour).Unix(), GrantedScopes: []string{"chatgpt.tokens.use.direct"}, PlanUseEnabled: true}
	raw, _ := json.Marshal(tokens)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	result["success"] = true
	s := &Server{store: st, oauthJobs: map[string]map[string]any{"job": {"job_id": "job"}}}
	s.finishMailAccountSIWCJob("job", email, result, nil)
	if s.oauthJobs["job"]["status"] != "success" {
		t.Fatal(s.oauthJobs["job"])
	}
	jobJSON, _ := json.Marshal(s.oauthJobs["job"])
	if strings.Contains(string(jobJSON), "siwc-at") || strings.Contains(string(jobJSON), "siwc-rt") {
		t.Fatal("tokens leaked in job polling")
	}
	p, saved, _ := st.MailAccountCredential(email)
	if !p.SIWCPresent || saved.AccessToken != original.AccessToken || saved.RefreshToken != original.RefreshToken || saved.GptPassword != original.GptPassword || saved.TotpSecret != original.TotpSecret || p.OAuthStatus != before.OAuthStatus {
		t.Fatal("SIWC changed ordinary login or Pro state")
	}
	if saved.SIWC == nil || saved.SIWC.ClientID != tokens.ClientID {
		t.Fatal("SIWC metadata missing")
	}
	for _, format := range []string{"raw", "siwc"} {
		r := httptest.NewRequest("GET", "/credentials?format="+format, nil)
		r.SetPathValue("email", email)
		w := httptest.NewRecorder()
		s.exportMailAccountCredentials(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("view/export failed: %d %s", w.Code, w.Body.String())
		}
		for _, expected := range []string{"siwc-at", "siwc-rt", "oaiapp_fixture"} {
			if !strings.Contains(w.Body.String(), expected) {
				t.Fatalf("missing %s", expected)
			}
		}
		if format == "siwc" && (strings.Contains(w.Body.String(), "codex-at") || strings.Contains(w.Body.String(), "gpt-password") || strings.Contains(w.Body.String(), "totp-secret")) {
			t.Fatal("SIWC export contains unrelated credentials")
		}
	}
	// Standard token refresh, manual edits and re-import all retain SIWC.
	if err = st.SaveMailAccountOAuth(email, "codex-new-at", "codex-new-rt"); err != nil {
		t.Fatal(err)
	}
	p, saved, _ = st.MailAccountCredential(email)
	newPassword := "updated-password"
	_, err = st.PatchMailCredentials(email, store.MailCredentialsPatch{Revision: st.MailCredentialsRevision(p, saved), GPTPassword: &newPassword})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SaveMailAccount(model.MailAccountProfile{Email: email}, original)
	if err != nil {
		t.Fatal(err)
	}
	_, saved, _ = st.MailAccountCredential(email)
	if saved.SIWC == nil || saved.SIWC.AccessToken != tokens.AccessToken {
		t.Fatal("ordinary operations lost SIWC")
	}
	// Invalid/mismatched token results never overwrite the saved bundle.
	result["email"] = "other@example.com"
	s.finishMailAccountSIWCJob("job", email, result, nil)
	if s.oauthJobs["job"]["status"] != "failed" {
		t.Fatal("mismatched identity accepted")
	}
	_, saved, _ = st.MailAccountCredential(email)
	if saved.SIWC.AccessToken != tokens.AccessToken {
		t.Fatal("failed flow changed credentials")
	}
}

func TestSIWCExportWithoutCodexTokens(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	email := "siwconly@example.com"
	_, _ = st.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email})
	s := &Server{store: st}
	r := httptest.NewRequest("GET", "/credentials?format=siwc", nil)
	r.SetPathValue("email", email)
	w := httptest.NewRecorder()
	s.exportMailAccountCredentials(w, r)
	if w.Code != 409 {
		t.Fatal("missing SIWC should not export")
	}
	err = st.SaveMailAccountSIWC(email, model.SIWCCredentials{Email: email, AuthMode: "siwc", AccessToken: "only-at", RefreshToken: "only-rt", IDToken: "only-id", ClientID: "oaiapp_only", ExtAgentHostID: "urn:uuid:host", Subject: "subject", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.exportMailAccountCredentials(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
