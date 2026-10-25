package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
	"context"
	"crypto/des"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ejhTestServer(t *testing.T, status int) (*Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/authenticate" {
			json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": map[string]any{"authToken": "fixture-token", "validity": 3600000}})
			return
		}
		if r.Header.Get("Authorization") != "fixture-token" {
			t.Error("wrong token header")
		}
		var envelope map[string]string
		if e := json.NewDecoder(r.Body).Decode(&envelope); e != nil {
			t.Error(e)
		}
		encrypted, e := base64.StdEncoding.DecodeString(envelope["param"])
		if e != nil || len(encrypted)%8 != 0 {
			t.Error("invalid cipher")
			return
		}
		block, _ := des.NewCipher([]byte("fixture-"))
		plain := make([]byte, len(encrypted))
		for i := 0; i < len(plain); i += 8 {
			block.Decrypt(plain[i:i+8], encrypted[i:i+8])
		}
		pad := int(plain[len(plain)-1])
		if pad < 1 || pad > 8 {
			t.Error("padding")
			return
		}
		plain = plain[:len(plain)-pad]
		var body struct {
			Method    string `json:"method"`
			Version   string `json:"v"`
			Timestamp int64  `json:"timestamp"`
		}
		if e = json.Unmarshal(plain, &body); e != nil {
			t.Error(e)
		}
		if body.Method != "card.open.info" || body.Version != "3.0" || time.Now().Unix()-body.Timestamp > 10 {
			t.Error("wrong business envelope")
		}
		calls.Add(1)
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": ejh.Card{OrderNo: "fixture-order", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "1", CardCurrency: "USD"}})
	}))
	t.Cleanup(up.Close)
	st, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{store: st}
	s.ejh.client = ejh.New()
	s.ejh.client.HTTP = up.Client()
	cfg := ejh.DefaultSettings()
	cfg.Enabled = true
	cfg.URL = up.URL
	cfg.AppKey = "fixture"
	if _, e = st.SaveEJHSettings(cfg, "fixture-secret"); e != nil {
		t.Fatal(e)
	}
	return s, calls
}

func TestEJHAuthorizationRedactionAndCSRF(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("no supplier requests") })
	remote := ejh.Card{OrderNo: "redact-card", Number: "4242424242424242", CVV: "919", Validity: "1299", Status: "1"}
	if _, err := st.ImportEJHCard(remote, true); err != nil {
		t.Fatal(err)
	}
	if err := st.EJHPut("operation", "redact", ejh.Operation{ID: "redact", Result: &remote}); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	routes := [][2]string{{"GET", "settings"}, {"PUT", "settings"}, {"GET", "dashboard"}, {"POST", "open"}, {"POST", "test"}, {"POST", "balance"}, {"POST", "sync"}, {"POST", "cards/x/action"}, {"POST", "cards/x/private"}, {"POST", "cards/x/trades"}, {"POST", "operations/x/resolve"}, {"PUT", "trades/x/match"}, {"POST", "trades/x/unmatch"}}
	for _, route := range routes {
		r := httptest.NewRequest(route[0], "/api/ejh/"+route[1], strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(route, w.Code)
		}
	}
	cookie := &http.Cookie{Name: authCookieName, Value: s.createSession()}
	r := httptest.NewRequest("GET", "/api/ejh/dashboard", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), remote.Number) || strings.Contains(w.Body.String(), `"cvvPassword":"919"`) {
		t.Fatal("sensitive dashboard leak", w.Code)
	}
	for _, tc := range []struct {
		origin, ct, site string
		code             int
	}{{"https://attacker.invalid", "application/json", "", 403}, {"", "application/json", "cross-site", 403}, {"", "text/plain", "", 415}, {"http://example.com", "application/json", "same-origin", 400}} {
		r = httptest.NewRequest("POST", "/api/ejh/open", strings.NewReader(`{}`))
		r.AddCookie(cookie)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.ct)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}

func TestEJHForceMergeTailAndDisabledSetting(t *testing.T) {
	for _, mode := range []string{"timeout", "disabled", "retry", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("must not pay or login") })
			configureProAutoFixture(t, s)
			cfg, _, _, err := st.ProSettings()
			if err != nil {
				t.Fatal(err)
			}
			cfg.ForceMergeEnabled = mode != "disabled"
			cfg.ForceMergeMinutes = 1
			cfg.RetryCount = 2
			if _, err = st.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			started := time.Now().Add(-2 * time.Minute)
			_, err = st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.CaptureProPushTimeout(cfg)
				p.ProPushStartedAt = &started
				p.PushStatus = "completed"
				p.ProAuto = model.ProAutoState{ID: "tail", Status: "running", QuotaUsedThreshold: 80, QuotaIntervalSeconds: 30, Steps: map[string]string{"push": "completed", "oauth": "completed", "recharge": "completed"}}
				if mode == "disabled" {
					p.ProMergeTrigger = "push_timeout"
					p.ProAuto.Steps["quota"] = "skipped"
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			quotaCalls, mergeCalls := 0, 0
			s.proAutoHooks = &proAutomationHooks{Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
				quotaCalls++
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 1} })
			}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
				mergeCalls++
				if mode == "unknown" {
					st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.TransferStatus = "unknown" })
				}
				p, _, e := st.MailAccountCredential(email)
				if mode == "retry" || mode == "unknown" {
					e = errors.New("fixture merge failure")
				}
				return p, e
			}}
			err = s.runProAutomationTail(context.Background(), email)
			p, _, _ := st.MailAccountCredential(email)
			if mode == "unknown" {
				if err == nil {
					t.Fatal("unknown merge retried")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "disabled" {
				if mergeCalls != 0 || quotaCalls != 1 || p.ProAuto.Status != "waiting_quota" || p.ProAuto.NextCheckAt == nil || !p.ProAuto.NextCheckAt.After(time.Now()) {
					t.Fatal("disabled timeout bypassed quota", p.ProAuto)
				}
				return
			}
			if quotaCalls != 0 || mergeCalls != 1 || p.ProForceMergeAttempts != 1 {
				t.Fatal("tail did not use timeout", p.ProAuto)
			}
			if mode == "retry" && (p.ProAuto.Status != "waiting_quota" || p.ProAuto.NextCheckAt == nil || !p.ProAuto.NextCheckAt.After(time.Now())) {
				t.Fatal("safe merge failure not backed off")
			}
		})
	}
}
func TestEJHOpeningIdempotencyAndUnknown(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, calls := ejhTestServer(t, status)
			s.notifications.queue = make(chan notificationEvent, 10)
			input := ejh.OpenInput{CardType: "fixture", CardAmount: "20", CardCurrency: "USD", BillCurrency: "USD", MCC: "ecom", UseTime: 1, ActiveDate: "2099-12-31"}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.ejh.mu.Lock()
					defer s.ejh.mu.Unlock()
					op, e := s.ejhOpenLocked(context.Background(), "fixture-id", input, false)
					if e != nil {
						t.Error(e)
					}
					want := "completed"
					if status != 200 {
						want = "unknown"
					}
					if op.Status != want {
						t.Errorf("status=%s want %s", op.Status, want)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 1 {
				t.Fatalf("duplicate charges: %d", calls.Load())
			}
			if len(s.notifications.queue) != 1 {
				t.Fatal("duplicate opening result notices", len(s.notifications.queue))
			}
			event := <-s.notifications.queue
			wantTitle := "EJH 开卡成功"
			if status != 200 {
				wantTitle = "EJH 开卡待核实"
			}
			if event.Kind != "ejh_open" || event.Title != wantTitle || !strings.Contains(event.Text, "20 USD") || strings.Contains(event.Text, "4242424242424242") {
				t.Fatal("incorrect or sensitive opening notice", event)
			}
			if status != 200 {
				_, e := s.ejhOpenLocked(context.Background(), "new-id", input, false)
				if e == nil || calls.Load() != 1 {
					t.Fatal("unknown opening allowed new purchase")
				}
			}
		})
	}
}
func TestEJHForceMergeGuard(t *testing.T) {
	now := time.Now()
	started := now.Add(-61 * time.Minute)
	cfg := model.DefaultProSettings()
	cfg.ForceMergeEnabled = true
	cfg.ForceMergeMinutes = 60
	base := model.MailAccountProfile{ManagementScope: "pro", PushStatus: "completed", ProPushStartedAt: &started}
	base.CaptureProPushTimeout(cfg)
	if !forceMergeDue(base, cfg, now) {
		t.Fatal("missed deadline")
	}
	for _, change := range []func(*model.MailAccountProfile){func(p *model.MailAccountProfile) { p.PushStatus = "failed" }, func(p *model.MailAccountProfile) { p.ProAuto.Status = "stopped" }, func(p *model.MailAccountProfile) { p.TransferStatus = "unknown" }, func(p *model.MailAccountProfile) { p.ProWorkflowRunning = true }, func(p *model.MailAccountProfile) { p.ProForceMergeAttempts = cfg.RetryCount + 1 }, func(p *model.MailAccountProfile) { p.TransferStatus = "completed"; p.RemoveStatus = "completed" }} {
		p := base
		change(&p)
		if forceMergeDue(p, cfg, now) {
			t.Fatal("unsafe deadline trigger")
		}
	}
}

func TestEJHAutoOpeningBudgetAndExistingCard(t *testing.T) {
	for _, budget := range []string{"19.99", "20"} {
		t.Run(budget, func(t *testing.T) {
			s, calls := ejhTestServer(t, 200)
			s.notifications.queue = make(chan notificationEvent, 10)
			cfg, _, err := s.store.EJHSettings()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Templates = []ejh.Template{{ID: "fixture", Name: "fixture", ValidDays: 30, OpenInput: ejh.OpenInput{CardType: "fixture", CardAmount: "20", CardCurrency: "USD", BillCurrency: "USD", UseTime: 1, MCC: "ecom"}}}
			if _, err = s.store.SaveEJHSettings(cfg, ""); err != nil {
				t.Fatal(err)
			}
			pro := model.DefaultProSettings()
			pro.EJHAutoOpen = true
			pro.EJHTemplateID = "fixture"
			pro.EJHDailyOpenLimit = 1
			pro.EJHDailyAmountLimit = budget
			if _, err = s.store.SaveProSettings(pro, "", ""); err != nil {
				t.Fatal(err)
			}
			card, err := s.ensureEJHCard(context.Background())
			if budget == "19.99" {
				if err == nil || calls.Load() != 0 {
					t.Fatal("budget bypass")
				}
				return
			}
			if err != nil || !card.Available || calls.Load() != 1 {
				t.Fatal("opening failed", err)
			}
			if _, err = s.ensureEJHCard(context.Background()); err != nil || calls.Load() != 1 {
				t.Fatal("existing card not reused", err)
			}
			if len(s.notifications.queue) != 1 {
				t.Fatal("automatic opening sent duplicate or no notice")
			}
			if event := <-s.notifications.queue; event.Kind != "ejh_open" || !strings.Contains(event.Text, "自动开卡") {
				t.Fatal(event)
			}
			card.Enabled = false
			_, secret, err := s.store.GPTPayCard(card.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.SaveGPTPayCard(card, secret); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ensureEJHCard(context.Background()); err == nil || calls.Load() != 1 {
				t.Fatal("bought another card after unusable unused card")
			}
		})
	}
}
