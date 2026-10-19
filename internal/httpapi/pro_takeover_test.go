package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
)

func TestTakeoverTwoProjectsWaitForEJHBeforeScheduledWithdraw(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-19T16:49:00+08:00")
	var mu sync.Mutex
	memberships := map[int64][]int64{101: {7, 99}, 201: {42}}
	writes, creates := 0, 0
	var rejectNewGroups bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		write := func(v any) { json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": v}) }
		account := func(id int64) map[string]any {
			return map[string]any{"id": id, "name": fmt.Sprint(id), "platform": "openai", "type": "oauth", "status": "active", "schedulable": true, "concurrency": 80, "group_ids": memberships[id]}
		}
		switch r.URL.Path {
		case "/api/v1/auth/login":
			write(map[string]any{"access_token": "test"})
		case "/api/v1/admin/groups/all":
			write([]any{map[string]any{"id": 7, "name": "own"}, map[string]any{"id": 42, "name": "target"}, map[string]any{"id": 99, "name": "unrelated"}, map[string]any{"id": 43, "name": "second-target"}})
		case "/api/v1/admin/accounts":
			if r.Method == http.MethodPost {
				var input struct {
					Groups []int64 `json:"group_ids"`
				}
				json.NewDecoder(r.Body).Decode(&input)
				creates++
				memberships[102] = input.Groups
				write(account(102))
				return
			}
			group, _ := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
			rows := []any{}
			for id, ids := range memberships {
				if slices.Contains(ids, group) {
					rows = append(rows, account(id))
				}
			}
			write(map[string]any{"items": rows, "total": len(rows)})
		case "/api/v1/admin/ops/concurrency":
			group, _ := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
			loads := map[string]any{}
			for id, ids := range memberships {
				if slices.Contains(ids, group) {
					loads[fmt.Sprint(id)] = map[string]any{"account_id": id, "max_capacity": 80, "current_in_use": 10, "waiting_in_queue": 0}
				}
			}
			write(map[string]any{"enabled": true, "timestamp": time.Now(), "account": loads})
		default:
			id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/accounts/"), 10, 64)
			if memberships[id] == nil {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodPut {
				if id == 102 && rejectNewGroups {
					w.WriteHeader(502)
					return
				}
				var v struct {
					Groups []int64 `json:"group_ids"`
				}
				json.NewDecoder(r.Body).Decode(&v)
				memberships[id] = v.Groups
				writes++
				if writes == 1 {
					w.WriteHeader(502)
					return
				}
			}
			write(account(id))
		}
	}))
	defer upstream.Close()
	provider, ps, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("payment must not run in handoff test") })
	receiver, rs, receiverEmail := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("payment must not run in handoff test") })
	provider.stopProAutomations()
	receiver.stopProAutomations()
	provider.stopNexovcc()
	receiver.stopNexovcc()
	provider.stopProQuality()
	receiver.stopProQuality()
	provider.proTakeover.now = func() time.Time { return now }
	receiver.proTakeover.now = func() time.Time { return now }
	pp := model.DefaultProSettings()
	pp.Provider = "sub2"
	pp.CardProvider = "nexovcc"
	pp.Sub2 = model.Sub2Settings{URL: upstream.URL, Email: "test@example.test", GroupIDs: []int64{7, 9}, AccountConcurrency: 80}
	pp, e := ps.SaveProSettings(pp, "sub-test", "")
	if e != nil {
		t.Fatal(e)
	}
	rp := pp
	rp.CardProvider = "ejh"
	rp.Sub2.GroupIDs = []int64{42, 88}
	if _, e = rs.SaveProSettings(rp, "sub-test", ""); e != nil {
		t.Fatal(e)
	}
	pc := restock.DefaultConfig()
	pc.Enabled = true
	pc.Mode = "auto"
	pc.GroupID = 7
	pc.TimeEnabled = false
	pc, e = ps.SaveProRestockConfig(pc)
	if e != nil {
		t.Fatal(e)
	}
	rc := pc
	rc.Version = 1
	rc.GroupID = 42
	rc.NoRestockEnabled = true
	rc.NoRestockWindows = []restock.DailyWindow{{Start: "17:00", End: "20:00", TakeoverEnabled: true}}
	rc.DailyEJHMergeEnabled = true
	rc.DailyEJHMergeTime = "17:30"
	rc, e = rs.SaveProRestockConfig(rc)
	if e != nil {
		t.Fatal(e)
	}
	donorHTTP := httptest.NewServer(provider.Handler())
	defer donorHTTP.Close()
	var peerOffline atomic.Bool
	receiverHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if peerOffline.Load() {
			w.WriteHeader(503)
			return
		}
		receiver.Handler().ServeHTTP(w, r)
	}))
	defer receiverHTTP.Close()
	tc := restock.DefaultTakeoverConfig()
	tc.Enabled = true
	tc.Role = "provider"
	tc.PeerURL = receiverHTTP.URL
	tc.Username = "admin"
	tc.PeerID = rs.TakeoverInstanceID()
	tc, e = ps.SaveProTakeoverConfig(tc, "admin")
	if e != nil {
		t.Fatal(e)
	}
	tr := restock.DefaultTakeoverConfig()
	tr.Enabled = true
	tr.LeadMinutes = 10
	tr.PeerURL = donorHTTP.URL
	tr.Username = "admin"
	tr.PeerID = ps.TakeoverInstanceID()
	tr.Groups = []int64{42, 43}
	tr, e = rs.SaveProTakeoverConfig(tr, "admin")
	if e != nil {
		t.Fatal(e)
	}
	enableNexovccPaymentFixture(t, ps)
	card, e := ps.SaveNexovccCard(nexovcc.Card{Serial: "takeover-test", Status: "1", Balance: "10"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if e != nil {
		t.Fatal(e)
	}
	order := gptpay.Order{ID: "takeover-paid", Email: email, CardID: card.ID, Status: "success", CreatedAt: now.Add(-time.Hour)}
	snapshot := gptpay.Snapshot{}
	snapshot.Input.Number = "4242424242424242"
	if e = ps.CreateGPTPayOrder(order, snapshot); e != nil {
		t.Fatal(e)
	}
	ref := model.ProDownstream(pp)
	if _, e = ps.UpdateProAccount(email, func(v *model.MailAccountProfile) {
		v.PushStatus = "completed"
		v.PushProvider = "sub2"
		v.Sub2AccountID = 101
		v.ProPushDownstream = &ref
	}); e != nil {
		t.Fatal(e)
	}
	tick := func() {
		provider.proTakeover.next = time.Time{}
		provider.tickProTakeover(context.Background())
		state, _ := ps.ProTakeoverState()
		if state.Error != "" {
			t.Fatal(state.Error)
		}
	}
	tick()
	st, _ := ps.ProTakeoverState()
	if st.Active() {
		t.Fatal("started too early")
	}
	now = now.Add(time.Minute)
	receiver.proRestock.tasks = []restock.Task{{Email: "already-running", Status: "external"}}
	tick()
	tick()
	st, _ = ps.ProTakeoverState()
	if now.Before(st.LeaseUntil) || writes != 0 {
		t.Fatal("did not wait for receiver's in-flight batch")
	}
	receiver.proRestock.tasks = nil
	tick()
	st, _ = ps.ProTakeoverState()
	if st.Phase != "preparing" || !now.Before(st.LeaseUntil) {
		t.Fatal("no preparatory grant", st)
	}
	mu.Lock()
	ok := slices.Equal(memberships[101], []int64{7, 99, 42, 43})
	mu.Unlock()
	if !ok {
		t.Fatal("temporary group not attached")
	}
	tick()
	if writes != 1 {
		t.Fatal("repeated attachment", writes)
	}
	// Runtime restart preserves the durable epoch/membership ledger.
	provider.proTakeover = proTakeoverRuntime{now: func() time.Time { return now }}
	tick()
	if writes != 1 {
		t.Fatal("restart replayed membership update")
	}
	// Edits affect the next handoff; the current epoch must keep its pinned
	// group set so its ledger can be withdrawn consistently at the deadline.
	tr.Groups = []int64{42}
	tr, e = rs.SaveProTakeoverConfig(tr, "")
	if e != nil {
		t.Fatal(e)
	}
	tick()
	info, e := receiver.takeoverInfo()
	if e != nil || !slices.Equal(info.Groups, []int64{42, 43}) {
		t.Fatal("editing saved choices changed an active handoff", info, e)
	}
	snap, e := provider.collectProRestock(context.Background(), pc, pp, "sub-test")
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Accounts) != 2 || !slices.Equal(snap.MonitorGroups, []int64{7, 42, 43}) {
		t.Fatal("union did not deduplicate", snap)
	}
	for _, a := range snap.Accounts {
		if a.ID == 101 && (a.SupplyExcluded || a.TakeoverPending) {
			t.Fatal("own donor unavailable")
		}
		if a.ID == 201 && !a.SupplyExcluded {
			t.Fatal("departing EJH counted as donor capacity")
		}
	}
	now, _ = time.Parse(time.RFC3339, "2026-10-19T17:00:00+08:00")
	tick()
	st, _ = rs.ProTakeoverState()
	if st.Phase != "active" || st.Block(tr, now) == "" {
		t.Fatal("receiver not paused")
	}
	// A fresh Global account retains every configured push group and gets all
	// receiver push groups in the push operation itself. A failed membership
	// request is retried by monitoring, never by creating another account.
	freshEmail := "new-global@example.test"
	_, credentials, _ := ps.MailAccountCredential(email)
	credentials.Email = freshEmail
	if _, e = ps.SaveMailAccount(model.MailAccountProfile{Email: freshEmail}, credentials); e != nil {
		t.Fatal(e)
	}
	if _, e = ps.UpdateMailAccountManagementScope(freshEmail, "pro"); e != nil {
		t.Fatal(e)
	}
	freshOrder := order
	freshOrder.ID, freshOrder.Email = "fresh-paid", freshEmail
	freshCard, err := ps.SaveNexovccCard(nexovcc.Card{Serial: "takeover-new", Status: "1", Balance: "10"}, &nexovcc.PrivateCard{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	freshOrder.CardID = freshCard.ID
	freshSnapshot := snapshot
	freshSnapshot.Input.Number = "5555555555554444"
	if e = ps.CreateGPTPayOrder(freshOrder, freshSnapshot); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	rejectNewGroups = true
	mu.Unlock()
	pushed, err := provider.performProPush(context.Background(), freshEmail)
	if err != nil || pushed.PushStatus != "completed" || pushed.Sub2AccountID != 102 {
		t.Fatal("successful creation was lost", err, pushed)
	}
	mu.Lock()
	if !slices.Equal(memberships[102], []int64{7, 9}) || creates != 1 {
		t.Fatal("original multi-group push changed", memberships[102], creates)
	}
	rejectNewGroups = false
	mu.Unlock()
	// Explicitly exercise the synchronous push hook before another timer.
	if e = provider.attachProPushTakeover(context.Background(), pp, "sub-test", pushed); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	if !slices.Equal(memberships[102], []int64{7, 9, 42, 43}) || creates != 1 {
		t.Fatal("new account did not cover all groups", memberships[102], creates)
	}
	mu.Unlock()
	tick()
	// Another operator adds an unrelated group during coverage. Preserve it.
	mu.Lock()
	memberships[101] = append(memberships[101], 88)
	mu.Unlock()
	// At the deadline, an offline receiver and a donor's own purchase must
	// preserve service and allow EJH recovery instead of withdrawing groups.
	provider.proRestock.tasks = []restock.Task{{Email: "donor-running", Status: "external"}}
	now, _ = time.Parse(time.RFC3339, "2026-10-19T20:00:00+08:00")
	peerOffline.Store(true)
	provider.proTakeover.next = time.Now().Add(time.Hour)
	receiver.tickProTakeover(context.Background())
	st, _ = rs.ProTakeoverState()
	if st.Phase != "recovering" || st.Block(tr, now) != "" {
		t.Fatal("receiver did not resume at the deadline", st)
	}
	provider.tickProTakeover(context.Background())
	st, _ = ps.ProTakeoverState()
	if st.Phase != "recovering" || st.Error == "" {
		t.Fatal("offline deadline not processed", st)
	}
	mu.Lock()
	ok = slices.Equal(memberships[101], []int64{7, 99, 42, 43, 88}) && slices.Equal(memberships[102], []int64{7, 9, 42, 43})
	mu.Unlock()
	if !ok {
		t.Fatal("withdrew coverage before EJH was ready")
	}
	peerOffline.Store(false)
	tick()
	st, _ = rs.ProTakeoverState()
	if st.Phase != "recovering" || st.Block(tr, now) != "" {
		t.Fatal("donor own pending purchase blocked EJH recovery", st)
	}
	if groups := restock.TakeoverGroups(pc.GroupID, tc, st); !slices.Equal(groups, []int64{7}) {
		t.Fatal("provider still replenishing receiver demand during recovery", groups)
	}
	// Recovery status and memberships survive a process restart.
	receiver.proTakeover = proTakeoverRuntime{now: func() time.Time { return now }}
	provider.proTakeover = proTakeoverRuntime{now: func() time.Time { return now }}
	tick()
	enableEJHPaymentFixture(t, rs)
	ejhCard, err := rs.ImportEJHCard(ejh.Card{OrderNo: "return-card", Number: "5555555555554444", Validity: "1299", CVV: "123", Status: "1", CardCurrency: "USD"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = rs.CreateGPTPayOrder(gptpay.Order{ID: "return-paid", Email: receiverEmail, CardID: ejhCard.ID, Status: "success", CreatedAt: now}, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "5555555555554444"}}}); err != nil {
		t.Fatal(err)
	}
	ref = model.ProDownstream(rp)
	if _, err = rs.UpdateProAccount(receiverEmail, func(v *model.MailAccountProfile) {
		v.PushStatus, v.PushProvider, v.Sub2AccountID, v.ProPushDownstream = "completed", "sub2", 201, &ref
	}); err != nil {
		t.Fatal(err)
	}
	sample := func() restock.Snapshot {
		t.Helper()
		now = now.Add(time.Second)
		v, err := receiver.collectProRestock(context.Background(), rc, rp, "sub-test")
		if err != nil {
			t.Fatal(err)
		}
		// The upstream fake uses wall time; normalize sample times to the test clock.
		v.At, v.CollectionStartedAt = now, now.Add(-time.Millisecond)
		if err = rs.SaveProRestockSnapshot(v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	// Payment and push succeeded, but one required group is still missing.
	receiver.observeTakeoverRecovery(rc, sample(), nil)
	st, _ = rs.ProTakeoverState()
	if st.ReadyCount != 0 || st.Phase != "recovering" {
		t.Fatal("incomplete push memberships counted as ready", st)
	}
	mu.Lock()
	memberships[201] = []int64{42, 43}
	mu.Unlock()
	receiver.observeTakeoverRecovery(rc, sample(), []restock.Task{{Status: "verifying"}})
	st, _ = rs.ProTakeoverState()
	if st.ReadyCount != 0 {
		t.Fatal("receiver's unfinished batch counted as ready")
	}
	v := sample()
	receiver.observeTakeoverRecovery(rc, v, nil)
	receiver.observeTakeoverRecovery(rc, v, nil)
	st, _ = rs.ProTakeoverState()
	if st.ReadyCount != 1 || st.Phase != "recovering" {
		t.Fatal("cached sample counted twice", st)
	}
	// A failed monitor round resets consecutive confirmation.
	v = sample()
	v.Valid = false
	receiver.observeTakeoverRecovery(rc, v, nil)
	st, _ = rs.ProTakeoverState()
	if st.ReadyCount != 0 {
		t.Fatal("failed sample did not reset confirmations")
	}
	for i := 1; i <= tr.ReturnConfirmations; i++ {
		receiver.observeTakeoverRecovery(rc, sample(), nil)
		st, _ = rs.ProTakeoverState()
		if st.ReadyCount != i || i < tr.ReturnConfirmations && st.Phase != "recovering" {
			t.Fatal("wrong confirmation count", st)
		}
	}
	if st.Phase != "released" || st.OwnReady != 1 {
		t.Fatal("one verified EJH did not release", st)
	}
	// A failed live round is not written over the last good durable snapshot.
	// It still must revoke a release that the provider has not acted on yet.
	v = sample()
	v.Valid = false
	receiver.observeTakeoverRecovery(rc, v, nil)
	st, _ = rs.ProTakeoverState()
	if st.Phase != "recovering" || st.ReadyCount != 0 {
		t.Fatal("failed live round retained release permission", st)
	}
	tick()
	for range tr.ReturnConfirmations {
		receiver.observeTakeoverRecovery(rc, sample(), nil)
	}
	// Revalidate immediately before telling the provider to remove memberships.
	v = sample()
	v.Valid = false
	if err = rs.SaveProRestockSnapshot(v); err != nil {
		t.Fatal(err)
	}
	tick()
	st, _ = rs.ProTakeoverState()
	if st.Phase != "recovering" {
		t.Fatal("withdrew on a failed last-minute monitor round", st)
	}
	for range tr.ReturnConfirmations {
		receiver.observeTakeoverRecovery(rc, sample(), nil)
	}
	tick()
	mu.Lock()
	ok = slices.Equal(memberships[101], []int64{7, 99, 88}) && slices.Equal(memberships[102], []int64{7, 9}) && slices.Equal(memberships[201], []int64{42, 43})
	mu.Unlock()
	if !ok {
		t.Fatal("handoff lost original, unrelated or EJH groups")
	}
	tick()
	st, _ = ps.ProTakeoverState()
	if st.Active() {
		t.Fatal("handoff not acknowledged")
	}
	final, _ := rs.ProRestockConfig()
	if !final.DailyEJHMergeEnabled || final.DailyEJHMergeTime != "17:30" || !final.NoRestockEnabled {
		t.Fatal("altered independent schedule")
	}
}

func TestTakeoverPeerAuthAndRedirectDoNotLeakCredentials(t *testing.T) {
	s, _, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) {})
	req := httptest.NewRequest("GET", "/api/pro-takeover/peer", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("anonymous peer allowed")
	}
	hits := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer redirect.Close()
	c := restock.DefaultTakeoverConfig()
	c.PeerURL = redirect.URL
	c.Username = "admin"
	var out takeoverPeerInfo
	if e := takeoverPeerCall(context.Background(), c, "secret", "GET", nil, &out); e == nil || hits != 0 {
		t.Fatal("redirect followed or accepted", e, hits)
	}
}

func TestTakeoverSaveIndependentFromEnableAndGroupSelection(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected payment") })
	s.stopProAutomations()
	s.stopNexovcc()
	s.stopProQuality()
	var groupName atomic.Value
	groupName.Store("renamed-group")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/v1/auth/login":
			data = map[string]any{"access_token": "test-token"}
		case "/api/v1/admin/groups/all":
			data = []any{map[string]any{"id": 42, "name": groupName.Load()}, map[string]any{"id": 43, "name": "additional-group"}}
		default:
			t.Error("unexpected Sub2 request", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer upstream.Close()
	p := model.DefaultProSettings()
	p.Provider = "sub2"
	p.CardProvider = "ejh"
	p.Sub2 = model.Sub2Settings{URL: upstream.URL, Email: "sub@example.test", GroupIDs: []int64{42}}
	if _, e := st.SaveProSettings(p, "sub-secret", ""); e != nil {
		t.Fatal(e)
	}
	rc := restock.DefaultConfig()
	rc.GroupID = 42
	if _, e := st.SaveProRestockConfig(rc); e != nil {
		t.Fatal(e)
	}
	var hits atomic.Int32
	var peerRole atomic.Value
	peerRole.Store("receiver")
	var peerID atomic.Value
	peerID.Store("another-project")
	var offline atomic.Bool
	peerHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		user, password, _ := r.BasicAuth()
		if user != "admin" || password != "peer-secret" {
			w.WriteHeader(401)
			return
		}
		writeAPI(w, 200, takeoverPeerInfo{ID: peerID.Load().(string), Role: peerRole.Load().(string), Downstream: model.ProDownstream(p), PushGroups: []int64{7}, CardProvider: "nexovcc"}, "")
	}))
	defer peerHTTP.Close()
	mux := http.NewServeMux()
	s.registerProTakeover(mux)
	mux.HandleFunc("GET /api/pro-restock/groups", s.getProRestockGroups)
	call := func(method, path string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(string(raw))))
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"peer-secret", "sub-secret", "test-token"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("secret leaked")
			}
		}
		return w
	}
	type request struct {
		restock.TakeoverConfig
		Password string `json:"password"`
	}
	c := restock.DefaultTakeoverConfig()
	c.Enabled = true
	c.PeerID = "forged-id"
	c.PeerURL = peerHTTP.URL
	c.Username = "admin"
	c.Groups = []int64{42, 43}
	offline.Store(true)
	call("PUT", "/api/pro-takeover/config", request{c, "peer-secret"}, 200)
	c, password, e := st.ProTakeoverConfig()
	if e != nil || c.Enabled || c.PeerID != "" || password != "peer-secret" || !slices.Equal(c.Groups, []int64{42, 43}) || hits.Load() != 0 {
		t.Fatal("save contacted peer, enabled automatically, lost selection or trusted peer identity", c, e, hits.Load())
	}
	enabled := func(on bool, status int) *httptest.ResponseRecorder {
		return call("POST", "/api/pro-takeover/enabled", map[string]any{"enabled": on, "version": c.Version}, status)
	}
	enabled(true, 400)
	offline.Store(false)
	if w := enabled(true, 400); !strings.Contains(w.Body.String(), "两边应分别") {
		t.Fatal("same role accepted")
	}
	after, _, _ := st.ProTakeoverConfig()
	if after.Enabled || after.Version != c.Version {
		t.Fatal("failed enable changed saved draft")
	}
	peerRole.Store("provider")
	call("POST", "/api/pro-takeover/test", request{c, ""}, 200)
	after, _, _ = st.ProTakeoverConfig()
	if after.Enabled || after.Version != c.Version {
		t.Fatal("connection test changed saved configuration")
	}
	enabled(true, 200)
	c, _, _ = st.ProTakeoverConfig()
	if !c.Enabled || c.PeerID != "another-project" {
		t.Fatal("not enabled/pinned", c)
	}
	info, e := s.takeoverInfo()
	if e != nil || !slices.Equal(info.Groups, []int64{42, 43}) {
		t.Fatal("selected groups replaced by normal pushes", info, e)
	}
	// Refresh gets current names even if cached push labels have not changed.
	for _, name := range []string{"renamed-group", "renamed-again"} {
		groupName.Store(name)
		if w := call("GET", "/api/pro-restock/groups", nil, 200); !strings.Contains(w.Body.String(), name) {
			t.Fatal("group name stale")
		}
	}
	// Saving an enabled configuration preserves activation without a peer call.
	offline.Store(true)
	beforeHits := hits.Load()
	c.LeadMinutes = 5
	call("PUT", "/api/pro-takeover/config", request{c, ""}, 200)
	c, _, _ = st.ProTakeoverConfig()
	if !c.Enabled || hits.Load() != beforeHits {
		t.Fatal("ordinary save changed enable state or contacted peer")
	}
	enabled(false, 200)
	c, _, _ = st.ProTakeoverConfig()
	if c.Enabled || hits.Load() != beforeHits {
		t.Fatal("offline disable failed")
	}
	// Enabling does not silently accept a removed group or repin another project.
	offline.Store(false)
	c.Groups = []int64{42, 999}
	call("PUT", "/api/pro-takeover/config", request{c, ""}, 200)
	c, _, _ = st.ProTakeoverConfig()
	if w := enabled(true, 400); !strings.Contains(w.Body.String(), "#999") {
		t.Fatal("missing group accepted")
	}
	c.Groups = []int64{43}
	call("PUT", "/api/pro-takeover/config", request{c, ""}, 200)
	c, _, _ = st.ProTakeoverConfig()
	if w := enabled(true, 400); !strings.Contains(w.Body.String(), "监控分组") {
		t.Fatal("uncovered monitor accepted")
	}
	c.Groups = []int64{42, 7}
	call("PUT", "/api/pro-takeover/config", request{c, ""}, 200)
	c, _, _ = st.ProTakeoverConfig()
	if w := enabled(true, 400); !strings.Contains(w.Body.String(), "常驻推送分组") {
		t.Fatal("overlapping selection accepted")
	}
	c.Groups = []int64{42}
	call("PUT", "/api/pro-takeover/config", request{c, ""}, 200)
	c, _, _ = st.ProTakeoverConfig()
	peerID.Store("changed-peer")
	if w := enabled(true, 400); !strings.Contains(w.Body.String(), "身份变化") {
		t.Fatal("changed identity accepted")
	}
	stale := c
	stale.Version--
	call("PUT", "/api/pro-takeover/config", request{stale, ""}, 409)
	call("POST", "/api/pro-takeover/enabled", map[string]any{"version": stale.Version, "enabled": false}, 409)
}
