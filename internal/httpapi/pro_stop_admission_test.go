package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProStopDuringCardIssuanceWinsOverStartup(t *testing.T) {
	for _, mode := range []string{"manual", "scheduled", "resume_stopped"} {
		t.Run(mode, func(t *testing.T) {
			s, issued, paid, email := allocationFixture(t)
			if mode == "resume_stopped" {
				if rec := payCall(t, s.stopProAutomation, "email", email, nil); rec.Code != 200 {
					t.Fatal(rec.Body.String())
				}
			}
			var logins atomic.Int32
			s.proAutoHooks = &proAutomationHooks{Session: func(context.Context, string, func(string, map[string]any) (any, error)) error {
				logins.Add(1)
				return errors.New("fixture stop")
			}}
			transport := s.ejh.client.HTTP.Transport
			s.ejh.client.HTTP.Transport = ejhBalanceTransport(func(r *http.Request) (*http.Response, error) {
				reply, err := transport.RoundTrip(r)
				if r.URL.Path == "/api/v3/card" {
					if rec := payCall(t, s.stopProAutomation, "email", email, nil); rec.Code != 200 {
						t.Errorf("stop: %d", rec.Code)
					}
				}
				return reply, err
			})
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"plan_code":"pro20","card_id":"test-card"}`))
			req.SetPathValue("email", email)
			if mode == "scheduled" {
				req = req.WithContext(context.WithValue(req.Context(), proScheduledContextKey{}, true))
			}
			rec := httptest.NewRecorder()
			s.startProAutomation(rec, req)
			profile := waitProAutoFinished(t, s, email)
			if rec.Code != 409 || logins.Load() != 0 || paid.Load() != 0 || !profile.ProAuto.StopRequested || profile.ProAuto.Status != "stopped" {
				t.Fatalf("late start erased stop: HTTP %d logins=%d paid=%d state=%+v", rec.Code, logins.Load(), paid.Load(), profile.ProAuto)
			}
			if issued.Load() != 1 {
				t.Fatalf("expected one already submitted card request, got %d", issued.Load())
			}
			// A later explicit start may clear the stop and reuse the issued card.
			s.ejh.client.HTTP.Transport = transport
			if rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"plan_code": "pro20"}); rec.Code != 202 {
				t.Fatal(rec.Body.String())
			}
			profile = waitProAutoFinished(t, s, email)
			if profile.ProAuto.StopRequested || issued.Load() != 1 {
				t.Fatal("explicit resume did not reuse the available card")
			}
		})
	}
}

func TestProStopWhileWaitingForCardPreventsIssuance(t *testing.T) {
	for _, gate := range []string{"admission", "issuer"} {
		t.Run(gate, func(t *testing.T) {
			s, issued, paid, email := allocationFixture(t)
			s.proAutoHooks = &proAutomationHooks{Session: func(context.Context, string, func(string, map[string]any) (any, error)) error {
				return errors.New("unexpected startup")
			}}
			accountValue, _ := s.proLocks.LoadOrStore("account:"+email, &sync.Mutex{})
			blocked, reached := &s.proCardAdmissionMu, accountValue.(*sync.Mutex)
			if gate == "issuer" {
				blocked, reached = &s.ejh.mu, &s.proCardAdmissionMu
			}
			blocked.Lock()
			released := false
			defer func() {
				if !released {
					blocked.Unlock()
				}
			}()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- payCall(t, s.startProAutomation, "email", email, map[string]any{"plan_code": "pro20", "card_id": "test-card"})
			}()
			deadline := time.Now().Add(3 * time.Second)
			for reached.TryLock() {
				reached.Unlock()
				if time.Now().After(deadline) {
					t.Fatal("startup did not reach card queue")
				}
				time.Sleep(time.Millisecond)
			}
			if rec := payCall(t, s.stopProAutomation, "email", email, nil); rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
			blocked.Unlock()
			released = true
			select {
			case rec := <-done:
				if rec.Code != 409 || issued.Load() != 0 || paid.Load() != 0 {
					t.Fatalf("stopped queue issued a card: HTTP %d issued=%d paid=%d", rec.Code, issued.Load(), paid.Load())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("startup did not leave card queue")
			}
		})
	}
}

func TestProAutoRejectsOutOfRangeQuotaBeforeMerge(t *testing.T) {
	for _, used := range []float64{-1, 101} {
		s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected payment") })
		s.proAutoCancel()
		s.proAutoWG.Wait()
		configureProAutoFixture(t, s)
		_, err := st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.ProAuto = model.ProAutoState{ID: "quota-audit", Status: "running", QuotaUsedThreshold: 80, QuotaIntervalSeconds: 30, Steps: map[string]string{"oauth": "completed", "push": "completed"}}
		})
		if err != nil {
			t.Fatal(err)
		}
		merges := 0
		s.proAutoHooks = &proAutomationHooks{Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
			return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: used} })
		}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
			merges++
			return model.MailAccountProfile{}, errors.New("unexpected merge")
		}}
		err = s.runProAutomationTail(context.Background(), email)
		p, _, readErr := st.MailAccountCredential(email)
		if err != nil || readErr != nil || merges != 0 || p.ProAuto.ProvisionedAt != nil || p.ProAuto.Status != "waiting_quota" || p.ProAuto.Error == "" {
			t.Errorf("invalid quota %v accepted: err=%v merges=%d state=%+v", used, err, merges, p.ProAuto)
		}
	}
}

func TestProQuotaEndpointRejectsInvalidPercentAndKeepsSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name        string
		five, seven float64
	}{
		{"negative seven day", 20, -1},
		{"excess seven day", 20, 101},
		{"negative five hour", -1, 100},
		{"excess five hour", 101, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProExecutionFixture(t)
			f.s.proAutoCancel()
			f.s.proAutoWG.Wait()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
				case "/api/v1/admin/accounts/42/stats":
					fmt.Fprint(w, `{"code":0,"data":{"summary":{"total_standard_cost":1}}}`)
				case "/api/v1/admin/openai/accounts/42/quota":
					fmt.Fprintf(w, `{"code":0,"data":{"rate_limit":{"primary_window":{"used_percent":%v,"limit_window_seconds":18000},"secondary_window":{"used_percent":%v,"limit_window_seconds":604800}}}}`, tc.five, tc.seven)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer up.Close()
			v, _, _, _ := f.s.store.ProSettings()
			v.Provider, v.Sub2.URL, v.Sub2.Email = "sub2", up.URL, "fixture@example.com"
			v.AutoMergeEnabled, v.QuotaUsedThreshold = true, 80
			if _, err := f.s.store.SaveProSettings(v, "fixture", ""); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				p.PushProvider = "sub2"
				p.Quota5H = &model.FreeQuotaWindow{UsedPercent: 10, LimitWindowSeconds: 18000}
				p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 20, LimitWindowSeconds: 604800}
				p.QuotaCheckedAt = &old
			}); err != nil {
				t.Fatal(err)
			}
			w := f.request(f.s.checkProAccountQuota, http.MethodPost, "/quota", "")
			p, _, err := f.s.store.MailAccountCredential(f.email)
			if err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || p.QuotaStatus != "failed" || p.Quota5H.UsedPercent != 10 || p.Quota7D.UsedPercent != 20 || p.QuotaCheckedAt == nil || !p.QuotaCheckedAt.Equal(old) || p.ProWorkflowRunning || p.SpaceMergedOnce {
				t.Fatalf("invalid quota replaced snapshot or started merge: HTTP %d profile=%+v", w.Code, p)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.calls) != 0 {
				t.Fatalf("invalid quota invoked merge: %v", f.calls)
			}
		})
	}
}
