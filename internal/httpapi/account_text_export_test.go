package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestAccountCredentialsTextExportScopesAndMissingFields(t *testing.T) {
	s, _ := nexFixtureServer(t)
	for i := 0; i < 125; i++ {
		email := fmt.Sprintf("export%03d@example.com", i)
		_, err := s.store.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email, GptPassword: "fixture-password", TotpSecret: "fixture-secret", AccessToken: "must-not-export", MailPassword: "not-gpt-password"})
		if err != nil {
			t.Fatal(err)
		}
	}
	email := "mail@example.com"
	if _, err := s.store.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, MailPassword: "wrong-password", PickupURL: "https://fixture.invalid"}); err != nil {
		t.Fatal(err)
	}
	r := payCall(t, s.exportProAccounts, "", "", map[string]any{"all": true, "format": "credentials", "query": "export"})
	if r.Code != 200 || len(strings.Split(strings.TrimSpace(r.Body.String()), "\r\n")) != 125 {
		t.Fatal("filtered export did not include all pages", r.Code)
	}
	if !strings.Contains(r.Body.String(), "export000@example.com----fixture-password----fixture-secret\r\n") || strings.Contains(r.Body.String(), "must-not-export") {
		t.Fatal("wrong credentials format")
	}
	if r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(r.Header().Get("Content-Disposition"), ".txt") {
		t.Fatal("download headers missing")
	}
	r = payCall(t, s.exportProAccounts, "", "", map[string]any{"emails": []string{"export001@example.com", "EXPORT001@example.com"}, "format": "credentials"})
	if r.Code != 200 || strings.Count(r.Body.String(), "\r\n") != 1 {
		t.Fatal("selection not deduplicated")
	}
	r = payCall(t, s.exportMailAccountCredentialsBatch, "", "", map[string]any{"emails": []string{email}, "format": "credentials"})
	if r.Code != 200 || r.Body.String() != email+"--------\r\n" {
		t.Fatal("missing values did not preserve columns")
	}
	r = payCall(t, s.exportProAccounts, "", "", map[string]any{"emails": []string{"export001@example.com", email}, "format": "credentials"})
	if r.Code != 409 || strings.Contains(r.Body.String(), "fixture-password") {
		t.Fatal("mixed scope leaked partial file")
	}
	if _, err := accountCredentialsTextLine(model.MailAccountCredentials{Email: email, GptPassword: "bad\nvalue"}); err == nil {
		t.Fatal("newline accepted")
	}
	if _, err := accountCredentialsTextLine(model.MailAccountCredentials{Email: email, TotpSecret: "bad----value"}); err == nil {
		t.Fatal("delimiter accepted")
	}
}
