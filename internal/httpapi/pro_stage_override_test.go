package httpapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProAutoStageEditsPersistWithoutExecution(t *testing.T) {
	f := newProExecutionFixture(t)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "auto", Status: "failed", Stage: "oauth", OrderID: "paid-order", StartedAt: time.Now().Add(-time.Hour), Steps: map[string]string{"recharge": "completed", "oauth": "failed"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	before, credentials, _ := f.s.store.MailAccountCredential(f.email)
	for _, stage := range []string{"login", "recharge", "oauth", "push", "quota", "merge"} {
		for _, status := range []string{"not_started", "pending", "failed", "completed", "skipped", "interrupted", "unknown"} {
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			body, _ := json.Marshal(map[string]any{"stage": stage, "status": status, "expected_status": p.ProStages().Steps[stage], "expected_updated_at": p.UpdatedAt})
			out := f.request(f.s.updateProAutoStage, "PUT", "/auto-stage", string(body))
			if out.Code != 200 {
				t.Fatalf("%s/%s: %s", stage, status, out.Body.String())
			}
			saved, c, _ := f.s.store.MailAccountCredential(f.email)
			if saved.ProStages().Steps[stage] != status || !saved.ProStages().Manual[stage] {
				t.Fatal("manual status not persisted")
			}
			if !reflect.DeepEqual(saved.ProAuto, before.ProAuto) || !reflect.DeepEqual(c, credentials) || saved.PushStatus != before.PushStatus || saved.TransferStatus != before.TransferStatus || saved.SpaceMergedOnce != before.SpaceMergedOnce {
				t.Fatal("editing changed execution or credentials")
			}
			rows, err := f.s.proAccountListWithCards([]model.MailAccountProfile{saved})
			if err != nil || rows[0].StageProgress.Steps[stage] != status {
				t.Fatal("list lost edit", err)
			}
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("annotation made remote requests")
	}
	if err := f.s.store.StartProManualStage(f.email, "oauth", "new-oauth"); err != nil {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.ProStages().Steps["oauth"] != "running" || p.ProStages().Manual["oauth"] {
		t.Fatal("new execution did not supersede edit")
	}
	if err := f.s.autoProUpdate(f.email, "quota", "waiting", ""); err != nil {
		t.Fatal(err)
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.ProStages().Steps["quota"] != "waiting" || p.ProStages().Manual["quota"] {
		t.Fatal("automatic result did not supersede edit")
	}
}

func TestProAutoStageRejectsStaleAndActiveEdits(t *testing.T) {
	f := newProExecutionFixture(t)
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	body := fmt.Sprintf(`{"stage":"login","status":"completed","expected_status":%q,"expected_updated_at":%q}`, p.ProStages().Steps["login"], p.UpdatedAt.Format(time.RFC3339Nano))
	unlock := f.s.lockProAccount(f.email)
	if out := f.request(f.s.updateProAutoStage, "PUT", "/auto-stage", body); out.Code != 409 {
		t.Fatal("active lock allowed edit")
	}
	unlock()
	if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.Status = "running" }); err != nil {
		t.Fatal(err)
	}
	if out := f.request(f.s.updateProAutoStage, "PUT", "/auto-stage", body); out.Code != 409 {
		t.Fatal("active workflow allowed edit")
	}
	if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.Status = "failed" }); err != nil {
		t.Fatal(err)
	}
	if out := f.request(f.s.updateProAutoStage, "PUT", "/auto-stage", body); out.Code != 409 {
		t.Fatal("stale version allowed edit")
	}
	for _, input := range []string{`{"stage":"login","status":"running"}`, `{"stage":"transfer","status":"completed"}`, `{"stage":"login","status":"waiting"}`} {
		if out := f.request(f.s.updateProAutoStage, "PUT", "/auto-stage", input); out.Code != 400 {
			t.Fatal("invalid edit accepted")
		}
	}
	saved, _, _ := f.s.store.MailAccountCredential(f.email)
	if len(saved.ProStageOverrides) != 0 {
		t.Fatal("rejected edit changed state")
	}
}
