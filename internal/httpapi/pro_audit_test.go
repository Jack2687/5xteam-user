package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestProAutoOrderReadFailureBlocksAdmission(t *testing.T) {
	dir := t.TempDir()
	s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected payment") }, dir)
	s.proAutoCancel()
	s.proAutoWG.Wait()
	configureProAutoFixture(t, s)
	_, secret, err := st.GPTPayCard("test-card")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateGPTPayOrder(gptpay.Order{ID: "uncertain", Email: email, CardID: "test-card", Status: "submission_unknown", CreatedAt: time.Now()}, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: secret}}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE gptpay_orders SET encrypted_snapshot='broken' WHERE id='uncertain'`); err != nil {
		t.Fatal(err)
	}
	var logins atomic.Int32
	s.proAutoHooks = &proAutomationHooks{Session: func(context.Context, string, func(string, map[string]any) (any, error)) error {
		logins.Add(1)
		return errors.New("fixture stop")
	}}
	rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"card_id": "test-card", "plan_code": "pro20"})
	waitProAutoFinished(t, s, email)
	if rec.Code != http.StatusInternalServerError || logins.Load() != 0 {
		t.Fatalf("unreadable active order admitted work: HTTP %d logins=%d %s", rec.Code, logins.Load(), rec.Body.String())
	}
}
