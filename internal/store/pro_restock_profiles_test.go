package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRestockProfilesWithoutCredentials(t *testing.T) {
	s := transferStore(t)
	now := time.Now()
	for i, email := range []string{"old@example.test", "new@example.test", "mail@example.test"} {
		scope := "pro"
		if i == 2 {
			scope = "mail"
		}
		_, err := s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: scope, CreatedAt: now.Add(time.Duration(i) * time.Minute)}, model.MailAccountCredentials{GptPassword: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Status reconciliation must not require decryption of unrelated credentials.
	if _, err := s.db.Exec("UPDATE mail_accounts SET encrypted_credentials='unreadable'"); err != nil {
		t.Fatal(err)
	}
	profiles, err := s.ProRestockProfiles()
	if err != nil || len(profiles) != 2 || profiles[0].Email != "new@example.test" || profiles[1].Email != "old@example.test" {
		t.Fatalf("profiles: %+v %v", profiles, err)
	}
	if _, err = s.db.Exec("UPDATE mail_accounts SET profile=json_set(profile,'$.pro_auto',42) WHERE email='new@example.test'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProRestockProfiles(); err == nil {
		t.Fatal("malformed workflow state silently treated as an empty pool")
	}
}

func TestProRestockReservationRejectsExpiredEvaluation(t *testing.T) {
	s := transferStore(t)
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, err := s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	d := restock.Decision{ID: "expired", At: time.Now(), Version: c.Version, DemandKnown: true, Suggested: 1,
		Snapshot: restock.Snapshot{At: time.Now().Add(-time.Minute), Valid: true, LoadValid: true}}
	if tasks, err := s.ReserveProRestock(d, "", 0); err == nil || len(tasks) != 0 {
		t.Fatalf("expired live evaluation reserved work: %+v %v", tasks, err)
	}
}
