package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/store"
)

func turbIntegrationTestToken(email string) string {
	claims := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_user_id":    "user-session",
			"chatgpt_account_id": "account-session",
			"chatgpt_plan_type":  "free",
		},
		"https://api.openai.com/profile": map[string]any{
			"email": email,
			"name":  "Session User",
		},
	}
	payload, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestTurbIntegrationDirectProAndCredentialUpdates(t *testing.T) {
	dataStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	server := &Server{store: dataStore}
	const email = "pro-import@example.com"
	token := turbIntegrationTestToken(email)
	push := func(option any, password string) map[string]any {
		t.Helper()
		input := map[string]any{"email": email, "access_token": token, "gpt_password": password}
		if option != nil {
			input["enter_pro"] = option
		}
		body, _ := json.Marshal(input)
		w := httptest.NewRecorder()
		server.importTurbRegistration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/turb/register", strings.NewReader(string(body))))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), token) || (password != "" && strings.Contains(w.Body.String(), password)) {
			t.Fatal("response leaked credentials")
		}
		var envelope struct{ Data map[string]any }
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	result := push(true, "initial-password-fixture")
	first, credentials, err := dataStore.MailAccountCredential(email)
	if err != nil || first.ManagementScope != "pro" || first.ProManagedAt == nil {
		t.Fatalf("direct Pro import failed: profile=%+v err=%v", first, err)
	}
	if credentials.AccessToken != token || credentials.GptPassword != "initial-password-fixture" {
		t.Fatal("direct Pro import lost credentials")
	}
	if result["management_scope"] != "pro" || result["entered_pro"] != true || result["mail_account_saved"] != true {
		t.Fatalf("unexpected response: %+v", result)
	}
	if len(dataStore.MailAccountsByManagementScope("mail")) != 0 || len(dataStore.MailAccountsByManagementScope("pro")) != 1 {
		t.Fatal("direct Pro import is visible in wrong list")
	}
	for _, option := range []any{true, false, nil} {
		result = push(option, "updated-password-fixture")
		current, creds, err := dataStore.MailAccountCredential(email)
		if err != nil || current.ID != first.ID || current.ManagementScope != "pro" || current.ProManagedAt == nil || !current.ProManagedAt.Equal(*first.ProManagedAt) {
			t.Fatalf("repeat import reset ownership or identity: %+v err=%v", current, err)
		}
		if creds.GptPassword != "updated-password-fixture" || len(dataStore.MailAccounts()) != 1 || len(dataStore.OpenAIAccounts()) != 1 {
			t.Fatal("repeat import lost credentials or duplicated an account")
		}
		if current.ProAuto.ID != "" || current.ProWorkflowRunning || len(dataStore.FreeAccounts()) != 0 {
			t.Fatal("import started Pro workflow or entered Team rotation")
		}
		if result["management_scope"] != "pro" || result["entered_pro"] != (option == true) {
			t.Fatalf("response did not reflect actual ownership: %+v", result)
		}
	}
	// An existing mail account follows the same transfer rule as a new import.
	if _, err := dataStore.UpdateMailAccountManagementScope(email, "mail"); err != nil {
		t.Fatal(err)
	}
	if result = push(false, ""); result["management_scope"] != "mail" || result["entered_pro"] != false {
		t.Fatalf("disabled option moved account: %+v", result)
	}
	if result = push(true, ""); result["management_scope"] != "pro" {
		t.Fatalf("existing mail account was not moved to Pro: %+v", result)
	}
	_, creds, _ := dataStore.MailAccountCredential(email)
	if creds.GptPassword != "updated-password-fixture" {
		t.Fatal("omitted password cleared existing credential")
	}
}

func TestTurbIntegrationProOptionValidationAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		email  string
		option any
		status int
	}{
		{"omitted", "default@example.com", nil, http.StatusOK},
		{"false", "default@example.com", false, http.StatusOK},
		{"string-not-boolean", "default@example.com", "true", http.StatusBadRequest},
		{"missing-email", "", true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			body := map[string]any{"access_token": turbIntegrationTestToken(tc.email)}
			if tc.option != nil {
				body["enter_pro"] = tc.option
			}
			raw, _ := json.Marshal(body)
			w := httptest.NewRecorder()
			(&Server{store: s}).importTurbRegistration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/turb/register", strings.NewReader(string(raw))))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == http.StatusOK {
				if len(s.MailAccountsByManagementScope("mail")) != 1 || len(s.MailAccountsByManagementScope("pro")) != 0 {
					t.Fatal("default ownership changed")
				}
			} else if len(s.MailAccounts()) != 0 || len(s.OpenAIAccounts()) != 0 {
				t.Fatal("invalid request wrote accounts")
			}
		})
	}
}

func TestTurbIntegrationSavesCompleteSessionWithoutTeamRotation(t *testing.T) {
	dataStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })

	const email = "session-import@example.com"
	token := turbIntegrationTestToken(email)
	body, _ := json.Marshal(map[string]any{
		"email":        email,
		"access_token": token,
		"totp_secret":  "JBSWY3DPEHPK3PXP",
		"chatgpt_session": map[string]any{
			"accessToken": token,
			"expires":     "2026-12-01T00:00:00.000Z",
			"user":        map[string]any{"email": email, "id": "user-session"},
			"account":     map[string]any{"id": "account-session", "planType": "free"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/turb/register", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	server := &Server{store: dataStore}
	server.importTurbRegistration(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, credentials, err := dataStore.MailAccountCredential(email)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.ChatGPTSessionPresent || !strings.Contains(credentials.ChatGPTSession, `"accessToken"`) {
		t.Fatalf("complete session was not saved: profile=%+v session=%q", profile, credentials.ChatGPTSession)
	}
	if !profile.TotpSecretPresent || credentials.TotpSecret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("totp secret was not saved: profile=%+v secret=%q", profile, credentials.TotpSecret)
	}
	if profile.RegistrationStatus != "success" {
		t.Fatalf("completed turb registration was not marked successful: profile=%+v", profile)
	}
	if accounts := dataStore.FreeAccounts(); len(accounts) != 0 {
		t.Fatalf("pure turb push entered Team rotation: %#v", accounts)
	}

	// A later push without 2FA must preserve the existing secret.
	body, _ = json.Marshal(map[string]any{"email": email, "access_token": token})
	req = httptest.NewRequest(http.MethodPost, "/api/integrations/turb/register", strings.NewReader(string(body)))
	rec = httptest.NewRecorder()
	server.importTurbRegistration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second push status=%d body=%s", rec.Code, rec.Body.String())
	}
	_, credentials, err = dataStore.MailAccountCredential(email)
	if err != nil || credentials.TotpSecret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("push without 2FA erased stored secret: credentials=%+v err=%v", credentials, err)
	}

	// A manual "push to Space" with newer credentials updates the same
	// mailbox record instead of creating a duplicate or a Team rotation row.
	body, _ = json.Marshal(map[string]any{
		"email": email, "access_token": token,
		"gpt_password": "new-chatgpt-password",
		"totp_secret":  "NEW-TOTP-SECRET",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/integrations/turb/register", strings.NewReader(string(body)))
	rec = httptest.NewRecorder()
	server.importTurbRegistration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update push status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, credentials, err = dataStore.MailAccountCredential(email)
	if err != nil || credentials.GptPassword != "new-chatgpt-password" || credentials.TotpSecret != "NEW-TOTP-SECRET" {
		t.Fatalf("manual push did not update password/2FA: profile=%+v credentials=%+v err=%v", profile, credentials, err)
	}
	if accounts := dataStore.FreeAccounts(); len(accounts) != 0 {
		t.Fatalf("manual update push unexpectedly entered Team rotation: %#v", accounts)
	}
}
