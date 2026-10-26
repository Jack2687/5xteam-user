package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProOAICustomSettingsAndLegacySave(t *testing.T) {
	s, st, _ := oaiFixture(t)
	cfg, _, _, _ := st.ProSettings()
	cfg.OAIConfirmationConcurrency = 100
	cfg.OAIInquiry = model.ProOAIInquiry{Prompt: "查询 {{email}}；末行 {{success_marker}} / {{failure_marker}} / {{review_marker}}", SuccessMarker: "成功!", FailureMarker: "未办理", ReviewMarker: "需复核"}
	body := map[string]any{"automation": cfg, "gptpay": map[string]any{"plan_code": "pro5"}}
	if r := payCall(t, s.saveProAutomationSettings, "", "", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := payCall(t, s.saveProAutomationSettings, "", "", map[string]any{"automation": model.DefaultProSettings(), "gptpay": map[string]any{"plan_code": "pro5"}}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	// Explicit settings work, and omitted fields retain the most recent value.
	if r := payCall(t, s.saveProAutomationSettings, "", "", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	legacy := map[string]any{}
	encoded, _ := json.Marshal(model.DefaultProSettings())
	raw := map[string]any{}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	for k, v := range raw {
		legacy[k] = v
	}
	delete(legacy, "oai_confirmation_concurrency")
	delete(legacy, "oai_inquiry")
	body["automation"] = legacy
	if r := payCall(t, s.saveProAutomationSettings, "", "", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	saved, _, _, _ := st.ProSettings()
	if saved.OAIConfirmationConcurrency != 100 || saved.OAIInquiry != cfg.OAIInquiry {
		t.Fatal("legacy client reset custom inquiry")
	}
	for _, limit := range []int{0, -1, 101} {
		legacy["oai_confirmation_concurrency"] = limit
		if r := payCall(t, s.saveProAutomationSettings, "", "", body); r.Code != 400 {
			t.Fatal("bad limit accepted", limit, r.Code)
		}
	}
}

func TestProOAIInquirySnapshotAndRestart(t *testing.T) {
	s, st, email := oaiFixture(t)
	cfg, _, _, _ := st.ProSettings()
	cfg.OAIInquiry = model.ProOAIInquiry{Prompt: "查询 {{email}}，最后一行 {{success_marker}}", SuccessMarker: "完成!", FailureMarker: "未办", ReviewMarker: "核实"}
	st.SaveProSettings(cfg, "", "")
	s.proOAILogin = func(ctx context.Context, email string) (map[string]any, error) {
		o := ctx.Value(proOAILoginKey{}).(*proOAILoginOptions)
		if !strings.Contains(o.Config["prompt"].(string), email) || !strings.Contains(o.Config["prompt"].(string), "完成!") {
			t.Error("prompt not rendered")
		}
		if _, err := o.Call("help_session", map[string]any{"session": map[string]any{"email": email, "cookies": "old-session"}}); err != nil {
			return nil, err
		}
		// Settings changed while the question is in flight: classify by its snapshot.
		cfg.OAIInquiry.SuccessMarker = "新成功"
		if _, err := st.SaveProSettings(cfg, "", ""); err != nil {
			return nil, err
		}
		return oaiHook(t, "退款已处理。\n完成!")(ctx, email)
	}
	if err := s.runProOAI(context.Background(), email, "merge_v2"); err != nil {
		t.Fatal(err)
	}
	before := oaiRecord(t, st, email)
	if before.Status != "confirmed" {
		t.Fatal("in-flight markers were overwritten")
	}
	if r := payCall(t, s.manageProOAI, "email", email, map[string]string{"mode": "merge_v2", "action": "restart"}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	v := oaiRecord(t, st, email)
	if !v.FreshLogin || v.SentAt != nil || v.Answer != "" || v.ConfirmedAt != nil || len(v.Replies) != 1 {
		t.Fatal("restart did not reset workflow and retain history")
	}
	if _, err := s.proOAICheckpoint(context.Background(), email, "merge_v2", before.Generation, false, "help_reply", map[string]any{"conversation_id": "thread-1", "message_id": "late", "answer": "退款已处理。\n完成!"}); err == nil {
		t.Fatal("old generation accepted")
	}
	s.proOAILogin = func(ctx context.Context, email string) (map[string]any, error) {
		o := ctx.Value(proOAILoginKey{}).(*proOAILoginOptions)
		session, _ := o.Config["session"].(map[string]any)
		if session != nil || o.Config["conversation_id"] != "" || o.Config["sent_at"] != nil {
			t.Error("restart reused old login/conversation")
		}
		if !strings.Contains(o.Config["prompt"].(string), "新成功") {
			t.Error("restart used old prompt")
		}
		return oaiHook(t, "退款已发起。\n新成功")(ctx, email)
	}
	if err := s.runProOAI(context.Background(), email, "merge_v2"); err != nil {
		t.Fatal(err)
	}
	if v = oaiRecord(t, st, email); v.Status != "confirmed" || len(v.Replies) != 2 {
		t.Fatal("restart lost result/history")
	}
}

func TestProOAIDispatchLimitDrainAndSameAccount(t *testing.T) {
	s, st, email := oaiFixture(t)
	cfg, _, _, _ := st.ProSettings()
	entered := make(chan string, 10)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); s.proAutoWG.Wait() }()
	s.proOAILogin = func(ctx context.Context, email string) (map[string]any, error) {
		entered <- email
		select {
		case <-release:
			return map[string]any{"success": true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	for i := 0; i < 4; i++ {
		e := fmt.Sprintf("dispatch-%d@example.com", i)
		st.SaveMailAccount(model.MailAccountProfile{Email: e}, model.MailAccountCredentials{Email: e})
		st.UpdateMailAccountManagementScope(e, "pro")
		st.UpdateProAccount(e, func(p *model.MailAccountProfile) {
			past := time.Now().Add(-time.Hour)
			v := model.NewProOAIConfirmation("merge_v2", cfg.OAIConfirmation["merge_v2"])
			v.Status = "waiting"
			v.CompletedAt = past
			v.NotBefore = &past
			v.MaxAttempts = 1
			p.ProOAIConfirmations = map[string]*model.ProOAIConfirmation{"merge_v2": v}
		})
	}
	// This earlier disabled mode must not consume slots or starve enabled work.
	st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		v := model.NewProOAIConfirmation("merge", model.ProOAIConfirmationRule{})
		past := time.Now().Add(-24 * time.Hour)
		v.CompletedAt = past
		v.NotBefore = &past
		v.Status = "waiting"
		v.MaxAttempts = 1
		p.ProOAIConfirmations["merge"] = v
		p.ProOAIConfirmations["merge_v2"].MaxAttempts = 1
	})
	take := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("dispatcher did not start expected work")
			}
		}
	}
	noMore := func() {
		t.Helper()
		select {
		case e := <-entered:
			t.Fatal("concurrency exceeded", e)
		case <-time.After(60 * time.Millisecond):
		}
	}
	s.dispatchProOAI(ctx)
	take(2)
	noMore()
	cfg.OAIConfirmationConcurrency = 1
	st.SaveProSettings(cfg, "", "")
	s.dispatchProOAI(ctx)
	noMore()
	cfg.OAIConfirmationConcurrency = 3
	st.SaveProSettings(cfg, "", "")
	s.dispatchProOAI(ctx)
	take(1)
	noMore()
	close(release)
	s.proAutoWG.Wait()
	// Two due modes for one account can only enter once, even at concurrency 100.
	cfg.OAIConfirmationConcurrency = 100
	rule := cfg.OAIConfirmation["merge"]
	rule.Enabled = true
	cfg.OAIConfirmation["merge"] = rule
	st.SaveProSettings(cfg, "", "")
	release = make(chan struct{})
	s.dispatchProOAI(ctx)
	take(2)
	noMore()
	close(release)
	s.proAutoWG.Wait()
}
