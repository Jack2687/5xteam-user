package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEJHManualCaseStatePhysicalCardAndOperationGuards(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	capacityCard(t, s, "manual-alias")
	settings, secret, err := s.EJHSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(settings, secret); err != nil {
		t.Fatal(err)
	}
	ids := []string{"manual-first", "manual-second"}
	emails := []string{"first@example.com", "second@example.com"}
	for i, id := range ids {
		transferSource(t, s, emails[i])
		order := transferOrder(emails[i], id, "success")
		order.Order.CardID = []string{card.ID, "manual-alias"}[i]
		if err = s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
			t.Fatal(err)
		}
	}
	settings.SingleActiveAccount = true
	if _, err = s.SaveEJHSettings(settings, secret); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEJHCaseState(ids[0], caseStateInput(t, s, ids[0], "returned")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{card.ID, "manual-alias"} {
		got, _, e := s.GPTPayCard(id)
		if e != nil || got.Available || got.ActiveAccounts != 1 || got.ActiveEmail != emails[1] {
			t.Fatal("released other account or alias", id, got, e)
		}
		if e = s.ReserveProCard("third@example.com", id, false); e == nil {
			t.Fatal("other occupant ignored", id)
		}
	}
	if err = s.EJHBeginOperation(ejh.Operation{ID: "blocked-cancel", CardID: card.ID, Method: "cancel", Status: "submitting"}); err == nil {
		t.Fatal("cancel ignored other account")
	}
	if _, err = s.SetEJHCaseState(ids[1], caseStateInput(t, s, ids[1], "returned")); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProCard(emails[0], "manual-alias", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEJHCaseState(ids[0], caseStateInput(t, s, ids[0], "unopened")); err == nil {
		t.Fatal("reservation allowed manual edit")
	}
	if err = s.ReleaseProCard(emails[0]); err != nil {
		t.Fatal(err)
	}
	op := ejh.Operation{ID: "pending-increase", CardID: card.ID, Method: "increase", Status: "submitting"}
	if err = s.EJHBeginOperation(op); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEJHCaseState(ids[1], caseStateInput(t, s, ids[1], "opened")); err == nil {
		t.Fatal("canonical card operation ignored by alias")
	}
	op.Status = "failed"
	if err = s.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEJHCaseState(ids[1], caseStateInput(t, s, ids[1], "returned")); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GPTPayCard("manual-alias")
	if err != nil || !got.Available || got.ActiveAccounts != 0 || got.OpenedAccounts != 2 {
		t.Fatal("all returned must release aliases without resetting usage", got, err)
	}
	if _, err = s.ImportEJHCard(ejh.Card{OrderNo: "fixture-card", Number: "4242424242424242", Validity: "1299", CVV: "123", Status: "5", CardCurrency: "USD"}, false); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProCard("third@example.com", "manual-alias", false); err == nil {
		t.Fatal("manual return bypassed frozen card")
	}
}

func caseStateInput(t *testing.T, s *Store, id, state string) EJHCaseStateInput {
	t.Helper()
	rows, err := s.EJHCases()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rows {
		if c.ID == id {
			var revision int64
			if c.StateOverride != nil {
				revision = c.StateOverride.Revision
			}
			return EJHCaseStateInput{State: state, Reason: "已核实账号实际回款情况", ExpectedState: c.State, ExpectedRevision: &revision, Confirm: true}
		}
	}
	t.Fatal("case missing")
	return EJHCaseStateInput{}
}

func TestEJHManualCaseStatesLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	card := ejhFixtureCard(t, s)
	email := "manual-state@example.com"
	transferSource(t, s, email)
	order := transferOrder(email, "manual-state", "success")
	order.Order.CardID = card.ID
	if err = s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"waiting", "returned", "opened", "unopened", "auto"} {
		row, err := s.SetEJHCaseState(order.Order.ID, caseStateInput(t, s, order.Order.ID, state))
		if err != nil {
			t.Fatal(state, err)
		}
		want := state
		if state == "auto" {
			want = "opened"
		}
		if row.State != want || row.AutomaticState != "opened" || len(row.Returns) != 0 || len(row.Amounts) != 0 {
			t.Fatal("manual state fabricated evidence", row)
		}
		got, _, err := s.GPTPayCard(card.ID)
		if err != nil {
			t.Fatal(err)
		}
		free := state == "returned" || state == "unopened"
		if got.Available != free || got.OpenedAccounts != 1 {
			t.Fatal(state, got)
		}
		if e := s.ReserveProCard("next@example.com", card.ID, false); (e == nil) != free {
			t.Fatal("wrong reservation gate", state, e)
		}
		if err = s.ReleaseProCard("next@example.com"); err != nil {
			t.Fatal(err)
		}
		op := ejh.Operation{ID: "cancel-" + state, CardID: card.ID, Method: "cancel", Status: "submitting"}
		if e := s.EJHBeginOperation(op); (e == nil) != free {
			t.Fatal("wrong cancellation gate", state, e)
		}
		if free {
			op.Status = "failed"
			if err = s.EJHPut("operation", op.ID, op); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Real return evidence is kept independently; manual opened takes precedence.
	ejhReceiveForOrder(t, s, card.ID, order.Order.ID)
	if _, err = s.SetEJHCaseState(order.Order.ID, caseStateInput(t, s, order.Order.ID, "opened")); err != nil {
		t.Fatal(err)
	}
	if err = s.EJHReconcile(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GPTPayCard(card.ID)
	if err != nil || got.Available {
		t.Fatal("sync/restart lost manual hold", got, err)
	}
	rows, err := s.EJHCases()
	if err != nil || rows[0].State != "opened" || rows[0].AutomaticState != "returned" || rows[0].Amounts["PHP"] != "5780.87" {
		t.Fatal("evidence overwritten", rows, err)
	}
	if _, err = s.SetEJHCaseState(order.Order.ID, caseStateInput(t, s, order.Order.ID, "auto")); err != nil {
		t.Fatal(err)
	}
	got, secret, err := s.GPTPayCard(card.ID)
	if err != nil || !got.Available {
		t.Fatal("auto restore failed", got, err)
	}
	cap := 1
	got.MaxAccounts = &cap
	if _, err = s.SaveGPTPayCard(got, secret); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEJHCaseState(order.Order.ID, caseStateInput(t, s, order.Order.ID, "returned")); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.GPTPayCard(card.ID)
	if err != nil || got.Available || got.OpenedAccounts != 1 {
		t.Fatal("manual return bypassed lifetime cap", got, err)
	}
	audits, err := EJHRecords[map[string]any](s, "match_audit")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range audits {
		if a["action"] == "manual_case_state" {
			n++
		}
	}
	if n != 8 {
		t.Fatal("missing manual audits", n)
	}
}

func TestEJHManualCaseStateGuardsAndConcurrentChanges(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	email := "guard@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "guard", "submission_unknown")
	o.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"unopened", "opened", "waiting", "returned"} {
		if _, err := s.SetEJHCaseState(o.Order.ID, caseStateInput(t, s, o.Order.ID, state)); err == nil {
			t.Fatal("unknown payment released", state)
		}
	}
	o.Order.Status = "failed"
	if err := s.UpdateGPTPayOrder(o.Order); err != nil {
		t.Fatal(err)
	}
	valid := caseStateInput(t, s, o.Order.ID, "opened")
	for _, mode := range []string{"invalid", "unconfirmed", "reason", "stale", "no_revision"} {
		in := valid
		switch mode {
		case "invalid":
			in.State = "other"
		case "unconfirmed":
			in.Confirm = false
		case "reason":
			in.Reason = " "
		case "stale":
			in.ExpectedState = "returned"
		case "no_revision":
			in.ExpectedRevision = nil
		}
		if _, err := s.SetEJHCaseState(o.Order.ID, in); err == nil {
			t.Fatal("invalid edit accepted", mode)
		}
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SetEJHCaseState(o.Order.ID, valid); err == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("stale writes accepted", accepted.Load())
	}
	got, _, err := s.GPTPayCard(card.ID)
	if err != nil || got.Available || got.ActiveAccounts != 1 {
		t.Fatal("failed order manual hold ignored", got, err)
	}
}

func TestEJHManualCaseStateTransfer(t *testing.T) {
	src := transferStore(t)
	card := ejhFixtureCard(t, src)
	email := "state-transfer@example.com"
	transferSource(t, src, email)
	o := transferOrder(email, "state-transfer", "success")
	o.Order.CardID = card.ID
	if err := src.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := src.SetEJHCaseState(o.Order.ID, caseStateInput(t, src, o.Order.ID, "returned")); err != nil {
		t.Fatal(err)
	}
	file, err := src.ExportProAccounts([]string{email}, model.ProDownstreamRef{})
	if err != nil {
		t.Fatal(err)
	}
	file = transferWire(t, file)
	if file.Accounts[0].Orders[0].ReturnConfirmed || file.Accounts[0].Orders[0].CaseStateOverride.State != "returned" {
		t.Fatal("manual return disguised as supplier evidence")
	}
	dst := transferStore(t)
	if _, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, false, model.ProDownstreamRef{}); err != nil {
		t.Fatal(err)
	}
	imported := ejhFixtureCard(t, dst)
	if err = dst.EJHBackfillCases(); err != nil {
		t.Fatal(err)
	}
	got, _, err := dst.GPTPayCard(imported.ID)
	if err != nil || !got.Available {
		t.Fatal("import lost manual state", got, err)
	}
	if _, err = dst.SetEJHCaseState(o.Order.ID, caseStateInput(t, dst, o.Order.ID, "auto")); err != nil {
		t.Fatal(err)
	}
	got, _, err = dst.GPTPayCard(imported.ID)
	if err != nil || got.Available {
		t.Fatal("auto restore kept manual release", got, err)
	}
}
