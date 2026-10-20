package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

type nexFixture struct {
	opens       atomic.Int32
	failDetail  bool
	unknown     bool
	noSerial    bool
	status      string
	cancelCalls int
	remark      string
}

func nexReply(v any) nexovcc.Response {
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": v})
	return nexovcc.Response{Status: 200, Body: string(raw)}
}
func nexFixtureServer(t *testing.T) (*Server, *nexFixture) {
	t.Helper()
	st, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{store: st, sessions: map[string]time.Time{}}
	f := &nexFixture{status: "1"}
	s.nexovcc.transport = func(_ context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		switch r.Path {
		case "/api/login":
			v := nexReply(map[string]any{"user_id": 7})
			v.Cookie = "fixture-cookie"
			return v, nil
		case "/api/getBalanceInfo":
			return nexReply([]any{map[string]any{"money_id": 3, "balance": "100.00", "freeze_balance": "0.00"}}), nil
		case "/api/findLogRecharge", "/api/findLogCardTrades", "/api/findLogWebFunds", "/api/findLogReportCardDay":
			return nexReply(map[string]any{"list": nil, "total": 0, "page": r.Body["page"], "pageSize": 100}), nil
		case "/api/product/findUserProductList":
			return nexReply(map[string]any{"list": []any{map[string]any{"ID": 267, "status": 1, "recharge_min": "1.00", "product_name": "fixture"}}}), nil
		case "/api/card/createUserCard":
			f.opens.Add(1)
			if r.Body["open_num"] != 1 || r.Body["money_id"] != 3 || r.Body["amount"] != "10.00" || r.Body["allow_consume_scene"] != "1,3,4,5,6,7,8,9" {
				t.Error("wrong open payload")
			}
			f.remark = fmt.Sprint(r.Body["remark"])
			if f.unknown {
				return nexovcc.Response{Status: 502}, nil
			}
			if f.noSerial {
				return nexReply(map[string]any{}), nil
			}
			return nexReply(map[string]any{"serial_no": "nex-test-serial"}), nil
		case "/api/card/getUserCardDetail":
			if f.failDetail {
				return nexovcc.Response{Status: 502}, nil
			}
			if r.Body["money_password"] != "fixture-fund" || r.Body["password"] != "fixture-fund" {
				t.Error("fund password missing")
			}
			return nexReply(map[string]any{"serial_no": r.Body["serial_no"], "status": f.status, "card_no": "4242424242424242", "cvv": "123", "expire_at_str": "12/99", "pid": 267, "remark": f.remark}), nil
		case "/api/card/getUserCardBalance":
			return nexReply(map[string]any{"balance": "10.00"}), nil
		case "/api/card/findUserCardList":
			return nexReply(map[string]any{"list": []any{map[string]any{"serial_no": "nex-test-serial", "status": f.status, "card_no": "4242424242424242", "balance": "10.00"}}}), nil
		case "/api/card/deleteUserCard":
			f.cancelCalls++
			f.status = "4"
			return nexReply(nil), nil
		case "/api/card/lockUserCard":
			f.status = "2"
			return nexReply(nil), nil
		case "/api/card/unLockUserCard":
			f.status = "1"
			return nexReply(nil), nil
		default:
			t.Errorf("unexpected endpoint %s", r.Path)
			return nexovcc.Response{Status: 500}, nil
		}
	}
	cfg := nexovcc.Defaults()
	cfg.HTTP522Retries = 0 // Retry policy is covered separately; fixtures never sleep.
	cfg.Enabled = true
	cfg.Templates = []nexovcc.Template{{ID: "template", Name: "fixture", ProductID: "267", Amount: "10.00", FeeBuffer: "1.00"}}
	if _, e = st.SaveNexovccSettings(cfg, nexovcc.Credentials{Login: "fixture-login", Password: "fixture-password", FundPassword: "fixture-fund"}, false); e != nil {
		t.Fatal(e)
	}
	if e = st.NexovccCache("products", []nexovcc.Product{{ID: "267", Minimum: "1.00"}}); e != nil {
		t.Fatal(e)
	}
	return s, f
}
func TestNexovccOpenIdempotenceAndImportRecovery(t *testing.T) {
	s, f := nexFixtureServer(t)
	f.failDetail = true
	op, e := s.openNexovccLocked(context.Background(), "nex-request1", "template", false)
	if e != nil || op.Status != "import_pending" || f.opens.Load() != 1 {
		t.Fatal(op, e)
	}
	again, e := s.openNexovccLocked(context.Background(), op.ID, "template", false)
	if e != nil || again.Status != "import_pending" || f.opens.Load() != 1 {
		t.Fatal("repeated supplier open", again, e)
	}
	if _, e = s.openNexovccLocked(context.Background(), "nex-request2", "template", false); e == nil {
		t.Fatal("new ID bypassed pending issue")
	}
	if f.opens.Load() != 1 {
		t.Fatal("duplicate card issued")
	}
	f.failDetail = false
	op, e = s.resolveNexovccLocked(context.Background(), nexovccInput{RequestID: op.ID, Outcome: "query"})
	if e != nil || op.Status != "succeeded" {
		t.Fatal(op, e)
	}
	card, _, e := s.store.GPTPayCard(op.CardID)
	if e != nil || !card.Available || card.Source != "nexovcc" || f.opens.Load() != 1 {
		t.Fatal(card, e)
	}
}
func TestNexovccUnknownOpenDoesNotRetryOrFallback(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			s, f := nexFixtureServer(t)
			f.unknown = !missing
			f.noSerial = missing
			op, e := s.openNexovccLocked(context.Background(), "nex-request1", "template", false)
			if e != nil || op.Status != "unknown" {
				t.Fatal(op, e)
			}
			if _, e = s.openNexovccLocked(context.Background(), "nex-request2", "template", false); e == nil {
				t.Fatal("unknown open repeated")
			}
			if f.opens.Load() != 1 {
				t.Fatal(f.opens.Load())
			}
		})
	}
}
func TestNexovccAutomaticIssuerSelectionAndBudget(t *testing.T) {
	s, f := nexFixtureServer(t)
	p := model.DefaultProSettings()
	p.CardProvider = "nexovcc"
	p.NexovccAutoOpen = true
	p.NexovccTemplateID = "template"
	p.NexovccDailyOpenLimit = 1
	p.NexovccDailyAmountLimit = "10.00"
	if _, _, e := s.store.SaveProAutomationSettings(p, gptpay.Settings{}, ""); e != nil {
		t.Fatal(e)
	}
	// An earlier Other card must not be selected for the chosen global issuer.
	if _, e := s.store.SaveGPTPayCard(gptpay.Card{ID: "other", Enabled: true}, gptpay.CardSecret{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099}); e != nil {
		t.Fatal(e)
	}
	p.NexovccAutoOpen = false
	if cards, e := s.automaticProCards(p); e != nil || len(cards) != 0 {
		t.Fatal("selected unrelated issuer while automatic buying is disabled", cards, e)
	}
	card, e := s.ensureProviderCard(context.Background())
	if e != nil || card.Source != "nexovcc" || f.opens.Load() != 1 {
		t.Fatal(card, e)
	}
	same, e := s.ensureProviderCard(context.Background())
	if e != nil || same.ID != card.ID || f.opens.Load() != 1 {
		t.Fatal("existing card not reused", same, e)
	}
	if cards, e := s.automaticProCards(p); e != nil || len(cards) != 1 || cards[0].ID != card.ID {
		t.Fatal("existing selected-issuer card omitted", cards, e)
	}
	p.CardProvider = "ejh"
	if cards, e := s.automaticProCards(p); e != nil || len(cards) != 1 || cards[0].ID != "other" {
		t.Fatal("default issuer selected global card", cards, e)
	}
	allocationAccount(t, s, "reserved@example.com")
	if e = s.store.ReserveProCard("reserved@example.com", card.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ensureProviderCard(context.Background()); e == nil || !strings.Contains(e.Error(), "预算") {
		t.Fatal("daily issuance budget bypass", e)
	}
	if f.opens.Load() != 1 {
		t.Fatal("budget checked after supplier mutation")
	}
}
func TestNexovccManualAndAutoIssueShareClaim(t *testing.T) {
	s, f := nexFixtureServer(t)
	f.unknown = true
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.nexovcc.mu.Lock()
			defer s.nexovcc.mu.Unlock()
			_, _ = s.openNexovccLocked(context.Background(), fmt.Sprintf("nex-concurrent%d", i), "template", false)
		}(i)
	}
	wg.Wait()
	if f.opens.Load() != 1 {
		t.Fatal("concurrent unknown opens", f.opens.Load())
	}
}
func TestNexovccAuthCSRFAndDashboardPrivacy(t *testing.T) {
	s, _ := nexFixtureServer(t)
	op, e := s.openNexovccLocked(context.Background(), "nex-request1", "template", false)
	if e != nil || op.Status != "succeeded" {
		t.Fatal(op, e)
	}
	handler := s.Handler()
	for _, path := range []string{"settings", "dashboard", "job", "cards/x/private"} {
		r := httptest.NewRequest("GET", "/api/nexovcc/"+path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unprotected endpoint", path, w.Code)
		}
	}
	cookie := &http.Cookie{Name: authCookieName, Value: s.createSession()}
	r := httptest.NewRequest("GET", "/api/nexovcc/dashboard", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"4242424242424242", "fixture-password", "fixture-fund", "fixture-cookie"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("dashboard leak")
		}
	}
	r = httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"open"}`))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.invalid")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF accepted", w.Code)
	}
}

func TestNexovccProAutomationRetriesSameCardThenPushes(t *testing.T) {
	var payments, sessions, pushes atomic.Int32
	s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := payments.Add(1)
		if n == 1 {
			fmt.Fprint(w, `{"code":0,"data":{"id":"global-unpaid","status":"failed","failureReason":"fixture rejected"}}`)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"id":"global-paid","status":"success"}}`)
	})
	s.proAutoCancel()
	s.proAutoWG.Wait()
	configureProAutoFixture(t, s)
	issuer, f := nexFixtureServer(t)
	s.nexovcc.transport = issuer.nexovcc.transport
	cfg, cred, err := issuer.store.NexovccSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.SaveNexovccSettings(cfg, cred, false); err != nil {
		t.Fatal(err)
	}
	if err = st.NexovccCache("products", []nexovcc.Product{{ID: "267", Minimum: "1.00"}}); err != nil {
		t.Fatal(err)
	}
	p, password, _, _ := st.ProSettings()
	p.CardProvider, p.NexovccAutoOpen, p.NexovccTemplateID = "nexovcc", true, "template"
	p.NexovccDailyOpenLimit, p.NexovccDailyAmountLimit = 2, "20.00"
	p.RetryCount, p.RetryIntervalSeconds = 1, 1
	if _, err = st.SaveProSettings(p, password, ""); err != nil {
		t.Fatal(err)
	}
	var firstCard, firstFlow string
	s.proAutoHooks = &proAutomationHooks{
		Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
			n := sessions.Add(1)
			profile, _, _ := st.MailAccountCredential(current)
			if n == 1 {
				firstCard, firstFlow = profile.ProAuto.CardID, profile.ProAuto.ID
			}
			if current != email || profile.ProAuto.CardID != firstCard || profile.ProAuto.ID != firstFlow {
				t.Error("retry changed identity")
			}
			card, _, err := st.GPTPayCard(profile.ProAuto.CardID)
			if err != nil || card.Source != "nexovcc" {
				t.Error("wrong card provider", err)
			}
			if _, err = rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(current, fmt.Sprint(n)), "user": map[string]any{"email": current}, "account": map[string]any{"id": "personal-id"}}}); err != nil {
				if n == 1 {
					held, _, readErr := st.GPTPayCard(firstCard)
					if readErr != nil || held.Available || held.PendingAccounts != 1 {
						t.Error("failed payment freed the original card before automatic retry", held, readErr)
					}
					if reserveErr := st.ReserveProCard("competitor@example.com", firstCard, false); reserveErr == nil {
						t.Error("another account stole the retry card")
					}
				}
				return err
			}
			_, err = rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(current, "new"), "refresh_token": "fixture-global-rt"}})
			return err
		},
		Push: func(_ context.Context, current string) (model.MailAccountProfile, error) {
			pushes.Add(1)
			return st.UpdateProAccount(current, func(p *model.MailAccountProfile) { p.PushStatus = "completed" })
		},
		Quota: func(_ context.Context, current string, _ bool) (model.MailAccountProfile, error) {
			return st.UpdateProAccount(current, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 10} })
		},
	}
	rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"provider": "tokenseek", "card_id": "", "plan_code": "pro20"})
	if rec.Code != 202 {
		t.Fatal(rec.Body.String())
	}
	profile := waitProAutoFinished(t, s, email)
	if sessions.Load() != 2 || payments.Load() != 2 || pushes.Load() != 1 || f.opens.Load() != 1 || profile.ProAuto.Status != "waiting_quota" {
		t.Fatal("global workflow", sessions.Load(), payments.Load(), pushes.Load(), f.opens.Load(), profile.ProAuto)
	}
	cases, err := st.NexovccCases()
	if err != nil {
		t.Fatal(err)
	}
	var paid string
	for _, c := range cases {
		if c.PaymentStatus == "success" {
			paid = c.ID
		}
	}
	if paid == "" {
		t.Fatal("paid global order missing")
	}
	if err = st.SetNexovccCase(paid, "returned", "已在卡台核实到账", 0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.proAccountListWithCards([]model.MailAccountProfile{profile})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	found := false
	for _, c := range rows[0].FinancialCases {
		if c.ID == paid {
			found = true
			if c.CardType != "nexovcc" || c.State != "returned" {
				t.Fatal("Pro list did not reflect global manual return", c)
			}
		}
	}
	if !found {
		t.Fatal("Pro list lost payment")
	}
}

func TestNexovccUnusedUnavailableCardStopsFurtherAutoIssue(t *testing.T) {
	s, f := nexFixtureServer(t)
	p := model.DefaultProSettings()
	p.CardProvider, p.NexovccAutoOpen, p.NexovccTemplateID = "nexovcc", true, "template"
	p.NexovccDailyOpenLimit, p.NexovccDailyAmountLimit = 10, "100.00"
	if _, _, e := s.store.SaveProAutomationSettings(p, gptpay.Settings{}, ""); e != nil {
		t.Fatal(e)
	}
	card, e := s.ensureProviderCard(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	local, secret, e := s.store.GPTPayCard(card.ID)
	if e != nil {
		t.Fatal(e)
	}
	local.Enabled = false
	if _, e = s.store.SaveGPTPayCard(local, secret); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ensureProviderCard(context.Background()); e == nil || !strings.Contains(e.Error(), "尚未使用") {
		t.Fatal("unused card did not stop further issue", e)
	}
	if f.opens.Load() != 1 {
		t.Fatal("issued multiple cards for stalled flow")
	}
}

func TestNexovccJobReportsBusinessRejectionAsFailure(t *testing.T) {
	s, _ := nexFixtureServer(t)
	t.Cleanup(s.stopNexovcc)
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/createUserCard" {
			return nexovcc.Response{Status: 200, Body: `{"code":1001,"message":"fixture rejected"}`}, nil
		}
		return base(ctx, r)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"open","template_id":"template","request_id":"nex-failed1","confirm":true}`))
	s.runNexovcc(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.nexovcc.wg.Wait()
	s.nexovcc.jobMu.Lock()
	job := s.nexovcc.job
	s.nexovcc.jobMu.Unlock()
	if job.Status != "failed" || job.Operation == nil || job.Operation.Status != "failed" || !strings.Contains(job.Message, "fixture rejected") {
		t.Fatal("business failure displayed as success", job)
	}
}

func TestNexovccSyncWithEmptyUpstreamCardList(t *testing.T) {
	s, f := nexFixtureServer(t)
	t.Cleanup(s.stopNexovcc)
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/findUserCardList" {
			return nexReply(map[string]any{"list": nil, "total": 0, "page": 1, "pageSize": 100}), nil
		}
		return base(ctx, r)
	}
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync"}`)))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.nexovcc.wg.Wait()
	s.nexovcc.jobMu.Lock()
	job := s.nexovcc.job
	s.nexovcc.jobMu.Unlock()
	if job.Status != "completed" || job.Done != job.Total {
		t.Fatalf("empty-card sync failed: %+v", job)
	}
	var balance nexovcc.Balance
	if err := s.store.NexovccCached("balance", &balance); err != nil || balance.Amount != "100.00" {
		t.Fatal(balance, err)
	}
	var products []nexovcc.Product
	if err := s.store.NexovccCached("products", &products); err != nil || len(products) != 1 {
		t.Fatal(products, err)
	}
	cards, err := s.store.NexovccCards()
	if err != nil || len(cards) != 0 || f.opens.Load() != 0 {
		t.Fatal(cards, err, f.opens.Load())
	}
}

func TestNexovccFundsAndCancellationRequireConfirmedReturn(t *testing.T) {
	s, f := nexFixtureServer(t)
	base := s.nexovcc.transport
	cardCents, accountCents, withdrawals := int64(1000), int64(10000), 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		switch r.Path {
		case "/api/getBalanceInfo":
			return nexReply([]any{map[string]any{"money_id": 3, "balance": nexovcc.Money(accountCents), "freeze_balance": "0.00"}}), nil
		case "/api/card/getUserCardBalance":
			return nexReply(map[string]any{"balance": nexovcc.Money(cardCents)}), nil
		case "/api/card/userCardWithdraw":
			withdrawals++
			n, e := nexovcc.Cents(fmt.Sprint(r.Body["amount"]))
			if e != nil || r.Body["money_id"] != 3 || r.Body["money_password"] != "fixture-fund" || r.Body["password"] != "fixture-fund" {
				t.Fatal("withdraw payload invalid", e)
			}
			cardCents -= n
			accountCents += n
			return nexReply(nil), nil
		case "/api/card/deleteUserCard":
			accountCents += cardCents
			cardCents = 0
		}
		return base(ctx, r)
	}
	op, e := s.openNexovccLocked(context.Background(), "nex-open1", "template", false)
	if e != nil || op.Status != "succeeded" {
		t.Fatal(op, e)
	}
	ctx := context.Background()
	if _, e = s.actionNexovccLocked(ctx, nexovccInput{Action: "cancel", CardID: op.CardID, RequestID: "nex-cancel1"}); e == nil {
		t.Fatal("cancel skipped necessary withdrawal")
	}
	if _, e = s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw", Amount: "10.00", CardID: op.CardID, RequestID: "nex-withdraw1"}); e == nil || withdrawals != 0 {
		t.Fatal("withdraw did not keep 1 USD")
	}
	in := nexovccInput{Action: "withdraw", Amount: "9.00", CardID: op.CardID, RequestID: "nex-withdraw2"}
	withdraw, e := s.actionNexovccLocked(ctx, in)
	if e != nil || withdraw.Status != "processing" || withdrawals != 1 {
		t.Fatal(withdraw, e, withdrawals)
	}
	if _, e = s.actionNexovccLocked(ctx, in); e != nil || withdrawals != 1 {
		t.Fatal("duplicate withdrawal", e, withdrawals)
	}
	if _, e = s.actionNexovccLocked(ctx, nexovccInput{Action: "cancel", CardID: op.CardID, RequestID: "nex-cancel2"}); e == nil {
		t.Fatal("cancel during unresolved withdrawal")
	}
	if _, e = s.resolveNexovccLocked(ctx, nexovccInput{RequestID: withdraw.ID, Outcome: "succeeded", Evidence: "已核实余额和主账户到账"}); e != nil {
		t.Fatal(e)
	}
	cancel, e := s.actionNexovccLocked(ctx, nexovccInput{Action: "cancel", CardID: op.CardID, RequestID: "nex-cancel3"})
	if e != nil || cancel.Status != "processing" || f.cancelCalls != 1 || cardCents != 0 || accountCents != 11000 {
		t.Fatal(cancel, e, f.cancelCalls, cardCents, accountCents)
	}
	// An increased account balance alone cannot confirm a card's return.
	if e = s.store.SaveNexovccFinance(nexovcc.Finance{At: time.Now(), Funds: []nexovcc.Fund{{ID: "close-return", Serial: cancel.Serial, Kind: 3002, Amount: "1.00", Currency: "USD", At: cancel.CreatedAt}}}); e != nil {
		t.Fatal(e)
	}
	cancel = s.verifyNexovccOperationLocked(ctx, cancel)
	if cancel.Status != "succeeded" || cancel.Evidence != "fund:close-return" {
		t.Fatal("same-card credit not recognized", cancel)
	}
	if _, e = s.actionNexovccLocked(ctx, nexovccInput{Action: "cancel", CardID: op.CardID, RequestID: "nex-cancel4"}); e == nil || f.cancelCalls != 1 {
		t.Fatal("closed card cancelled again", e, f.cancelCalls)
	}
}
