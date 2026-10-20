package httpapi

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccControlsDoNotQueryUnrelatedBalances(t *testing.T) {
	s, f := nexFixtureServer(t)
	base := s.nexovcc.transport
	checking := false
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if checking && (r.Path == "/api/getBalanceInfo" || r.Path == "/api/card/getUserCardBalance" || strings.Contains(r.Path, "findLog")) {
			t.Fatalf("card control queried unrelated finances: %s", r.Path)
		}
		if r.Path == "/api/card/updateCardRemark" {
			f.remark = nexovcc.String(r.Body["remark"])
			return nexReply(nil), nil
		}
		return base(ctx, r)
	}
	ctx := context.Background()
	opened, err := s.openNexovccLocked(ctx, "nex-controls-open1", "template", false)
	if err != nil || opened.Status != "succeeded" {
		t.Fatal(opened, err)
	}
	before, err := s.store.NexovccCard(opened.CardID)
	if err != nil {
		t.Fatal(err)
	}
	checking = true
	for _, action := range []string{"freeze", "unfreeze", "remark"} {
		op, err := s.actionNexovccLocked(ctx, nexovccInput{Action: action, CardID: opened.CardID, RequestID: "nex-control1-" + action, Remark: "fixture updated"})
		if err != nil || op.Status != "succeeded" {
			t.Fatal(action, op, err)
		}
		card, err := s.store.NexovccCard(opened.CardID)
		if err != nil || card.Balance != before.Balance || !card.BalanceAt.Equal(before.BalanceAt) {
			t.Fatal("control changed cached balance or its freshness", card, err)
		}
		if (action == "freeze" && card.Control != "frozen") || (action == "unfreeze" && card.Control != "active") || (action == "remark" && card.Remark != "fixture updated") {
			t.Fatal("control result was not saved", action, card)
		}
	}
}

func TestNexovccBalanceButtonQueriesOnlyRequestedCard(t *testing.T) {
	s, _ := nexFixtureServer(t)
	base := s.nexovcc.transport
	checking := false
	queries := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if checking {
			queries++
			if r.Path != "/api/card/getUserCardBalance" || r.Body["serial_no"] != "nex-test-serial" {
				t.Fatalf("balance button requested unrelated data: %s", r.Path)
			}
			return nexReply(map[string]any{"balance": "7.00"}), nil
		}
		return base(ctx, r)
	}
	ctx := context.Background()
	op, err := s.openNexovccLocked(ctx, "nex-balance-only1", "template", false)
	if err != nil || op.Status != "succeeded" {
		t.Fatal(op, err)
	}
	checking = true
	card, err := s.refreshNexovccBalanceLocked(ctx, op.CardID)
	if err != nil || card.Balance != "7.00" || queries != 1 {
		t.Fatal(card, queries, err)
	}
	card.Status = "4"
	if _, err = s.store.SaveNexovccCard(card, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.refreshNexovccBalanceLocked(ctx, op.CardID); err != nil || queries != 1 {
		t.Fatal("closed card balance was re-queried", queries, err)
	}
}

func TestNexovccOpeningCapacityUsesOnlyLocalCards(t *testing.T) {
	s, f := nexFixtureServer(t)
	cfg, _, _ := s.store.NexovccSettings()
	cfg.MaxOnlineCards = 4
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	productQueries := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/product/findUserProductList" {
			productQueries++
		}
		if r.Path == "/api/card/findUserCardList" || strings.Contains(r.Path, "findLog") {
			t.Fatalf("opening capacity contacted unrelated supplier endpoint: %s", r.Path)
		}
		return base(ctx, r)
	}
	op, err := s.openNexovccLocked(context.Background(), "nex-local-cap1", "template", false)
	if err != nil || op.Status != "succeeded" || f.opens.Load() != 1 || productQueries != 1 {
		t.Fatal(op, err, productQueries)
	}
}

func TestNexovccSettledDeletedCardsStopAndUnresolvedCardsContinue(t *testing.T) {
	s, _ := nexFixtureServer(t)
	now := time.Now()
	closed, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "closed", Status: "4"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "pending", Status: "4"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	f := nexovcc.Finance{At: now, AccountAt: now, Funds: []nexovcc.Fund{
		{ID: "closed-open", Serial: closed.Serial, Kind: 3001, Currency: "USD", Amount: "-10.00", At: now},
		{ID: "closed-return", Serial: closed.Serial, Kind: 3002, Currency: "USD", Amount: "10.00", At: now},
		{ID: "pending-open", Serial: pending.Serial, Kind: 3001, Currency: "USD", Amount: "-10.00", At: now},
	}}
	if err = s.store.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	calls := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if strings.Contains(r.Path, "findLog") {
			if r.Body["card_no"] != "4242424242424242" || (r.Path != "/api/findLogCardTrades" && r.Path != "/api/findLogWebFunds") {
				t.Fatalf("queried closed card or account history: %s", r.Path)
			}
			calls++
		}
		return base(ctx, r)
	}
	for i := 0; i < 2; i++ {
		if err = s.syncNexovccFinanceLocked(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var saved nexovcc.Finance
	if err = s.store.NexovccCached("finance", &saved); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(saved.Funds) != 2 || saved.Funds[0].Serial != closed.Serial {
		t.Fatal("closed history was re-read or erased", calls, saved)
	}
}

func TestNexovccBackgroundReadYieldsToOpening(t *testing.T) {
	s, f := nexFixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	base := s.nexovcc.transport
	var blocked atomic.Bool
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/getBalanceInfo" && blocked.CompareAndSwap(false, true) {
			close(started)
			<-ctx.Done()
			return nexovcc.Response{}, ctx.Err()
		}
		return base(ctx, r)
	}
	go func() { s.nexovcc.mu.Lock(); err := s.syncNexovccLocked(ctx); s.nexovcc.mu.Unlock(); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("sync did not start")
	}
	if err := s.lockNexovccOperation(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := s.openNexovccLocked(ctx, "nex-priority1", "template", false)
	s.nexovcc.mu.Unlock()
	if err != nil || op.Status != "succeeded" || f.opens.Load() != 1 {
		t.Fatal("opening blocked by read", op, err)
	}
	if err = <-done; !errors.Is(err, errNexovccReadYielded) {
		t.Fatal(err)
	}
	cfg, _, _ := s.store.NexovccSettings()
	state, err := s.nexovccSyncState(cfg)
	if err != nil || state.Failures != 0 {
		t.Fatal("yield counted as upstream failure", state, err)
	}
}

func TestNexovccCardFailureKeepsItsHistoryAndSavesOtherCards(t *testing.T) {
	s, _ := nexFixtureServer(t)
	pans := []string{"4242424242424242", "5555555555554444"}
	now := time.Now()
	f := nexovcc.Finance{At: now, AccountAt: now}
	for i, serial := range []string{"broken", "healthy"} {
		_, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: "1"}, &nexovcc.PrivateCard{Number: pans[i], CVV: "123", Month: 12, Year: 2099})
		if err != nil {
			t.Fatal(err)
		}
		f.Funds = append(f.Funds, nexovcc.Fund{ID: serial, Serial: serial, Kind: 3003, Currency: "USD", Amount: "-0.50", At: now})
	}
	if err := s.store.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/findLogWebFunds" && r.Body["card_no"] == pans[0] {
			return nexovcc.Response{Status: 502}, nil
		}
		return base(ctx, r)
	}
	if err := s.syncNexovccFinanceLocked(context.Background()); err == nil {
		t.Fatal("missing partial failure warning")
	}
	var saved nexovcc.Finance
	if err := s.store.NexovccCached("finance", &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Funds) != 1 || saved.Funds[0].ID != "broken" || saved.CardAt["healthy"].IsZero() {
		t.Fatal("card failure erased history or blocked other cards", saved)
	}
}

func TestNexovccAccountLedgerUsesIncrementalClockAndManualRefresh(t *testing.T) {
	s, _ := nexFixtureServer(t)
	now := time.Now()
	f := nexovcc.Finance{At: now, AccountAt: now, Funds: []nexovcc.Fund{{ID: "historical-fee", Kind: 3003, Currency: "USD", Amount: "-0.50", At: now.Add(-30 * 24 * time.Hour)}}}
	if err := s.store.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	calls := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if strings.Contains(r.Path, "findLog") {
			calls++
			if r.Path == "/api/findLogCardTrades" {
				t.Fatal("account refresh requested all card payments")
			}
			if r.Path != "/api/findLogReportCardDay" && (r.Body["time_min"] == nil || r.Body["time_min"] == "2020-01-01 00:00:00") {
				t.Fatal("incremental refresh requested entire history")
			}
		}
		return base(ctx, r)
	}
	if err := s.syncNexovccFinanceLocked(context.Background()); err != nil || calls != 0 {
		t.Fatal("automatic refresh ignored account cache", err, calls)
	}
	if err := s.syncNexovccFinanceLocked(context.Background(), true); err != nil || calls != 3 {
		t.Fatal("manual refresh did not update account window", err, calls)
	}
	var saved nexovcc.Finance
	if err := s.store.NexovccCached("finance", &saved); err != nil || len(saved.Funds) != 1 || saved.Funds[0].ID != "historical-fee" {
		t.Fatal("incremental refresh dropped historical fees", saved, err)
	}
}
