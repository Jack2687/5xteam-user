package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProDeletionSub2Confirmation(t *testing.T) {
	for _, scenario := range []string{"upstream401", "admin401", "upstream500", "upstream429", "live", "truncated", "incidental401", "wrong_target", "completed", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			f := newProExecutionFixture(t)
			enableDeletion(t, f)
			var probes atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/login" {
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"admin-token"}}`)
					return
				}
				if r.Method != "POST" || r.URL.Path != "/api/v1/admin/accounts/42/test" {
					t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				probes.Add(1)
				var input map[string]any
				if json.NewDecoder(r.Body).Decode(&input) != nil || input["model_id"] != "gpt-6.1-sol" || input["prompt"] != "hi" || input["endpoint"] != "original" || input["mode"] != "default" {
					t.Error("wrong account test parameters", input)
				}
				if scenario == "admin401" {
					w.WriteHeader(401)
					fmt.Fprint(w, `{"error":"API returned 401: not upstream"}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"test_start\",\"model\":\"gpt-6.1-sol\",\"data\":{\"custom_prompt\":true}}\n\n")
				switch scenario {
				case "upstream401":
					fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":\"API returned 401: invalidated oauth token\"}\n\n")
				case "upstream500", "upstream429":
					fmt.Fprintf(w, "data: {\"type\":\"error\",\"error\":\"API returned %s: unavailable\"}\n\n", strings.TrimPrefix(scenario, "upstream"))
				case "live":
					fmt.Fprint(w, "data: {\"type\":\"content\",\"text\":\"hello\"}\n\ndata: {\"type\":\"test_complete\",\"success\":true}\n\n")
				case "incidental401":
					fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":\"request id 401 timed out\"}\n\n")
				}
			}))
			defer remote.Close()
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.Sub2.URL, cfg.Sub2.Email = remote.URL, "admin@example.test"
			if _, err := f.s.store.SaveProSettings(cfg, "password", ""); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Add(-time.Minute)
			_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				p.PushProvider = "sub2"
				p.ProDeactivation = &model.ProDeactivation{Status: "queued", Stage: "confirm", SubmittedAt: &now, AcceptedAt: &now, Steps: map[string]string{"submit": "completed", "confirm": "waiting"}}
				ref := model.ProDownstream(cfg)
				p.ProPushDownstream = &ref
				if scenario == "wrong_target" {
					other := cfg
					other.Sub2.URL = "https://other.invalid"
					ref = model.ProDownstream(other)
				}
				if scenario == "completed" {
					p.ProDeactivation.ConfirmedAt, p.ProDeactivation.ConfirmationSource = &now, "manual"
				}
				p.ProAuto.StopRequested = scenario == "stopped"
			})
			if err != nil {
				t.Fatal(err)
			}
			f.s.proDeleteHooks = &proDeactivationHooks{Protocol: func(_ context.Context, in map[string]any, _ func(string, map[string]any) (any, error)) (map[string]any, error) {
				if in["check_only"] != true {
					t.Fatal("must never resubmit deletion")
				}
				return map[string]any{"status": "confirmed", "confirmation_source": "http_401"}, nil
			}, Login: func(context.Context, string) (map[string]any, error) {
				t.Fatal("must not login again")
				return nil, nil
			}}
			p, err := f.s.performProDeactivation(t.Context(), f.email)
			confirmed := scenario == "upstream401" || scenario == "completed"
			if p.DeletionConfirmed() != confirmed || (err == nil) != confirmed {
				t.Fatalf("confirmation=%t err=%v state=%+v", p.DeletionConfirmed(), err, p.ProDeactivation)
			}
			if confirmed {
				if p.ProDeactivation.NextCheckAt != nil {
					t.Fatal("confirmed account scheduled again")
				}
				before := probes.Load()
				if _, err := f.s.performProDeactivation(t.Context(), f.email); err != nil || probes.Load() != before {
					t.Fatal("repeated final probe", err)
				}
				if scenario == "upstream401" && p.ProDeactivation.ConfirmationSource != "sub2_http_401" {
					t.Fatal("missing evidence source")
				}
			} else if scenario != "stopped" && p.ProDeactivation.NextCheckAt == nil {
				t.Fatal("pending verification lost timer")
			}
			if (scenario == "completed" || scenario == "stopped" || scenario == "wrong_target") && probes.Load() != 0 {
				t.Fatal("must not probe")
			}
		})
	}
}
