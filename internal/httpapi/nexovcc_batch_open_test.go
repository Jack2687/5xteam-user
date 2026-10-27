package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccBatchOpenCountDisconnectAndReplay(t *testing.T) {
	s, f := nexFixtureServer(t)
	cfg, _, _ := s.store.NexovccSettings()
	cfg.BatchIntervalSeconds = 1
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	transport := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/createUserCard" {
			f.opens.Add(1)
			f.remark = fmt.Sprint(r.Body["remark"])
			return nexReply(map[string]any{"serial_no": fmt.Sprintf("batch-card-%d", f.opens.Load())}), nil
		}
		if r.Path == "/api/card/getUserCardDetail" {
			var index int
			fmt.Sscanf(fmt.Sprint(r.Body["serial_no"]), "batch-card-%d", &index)
			return nexReply(map[string]any{"serial_no": r.Body["serial_no"], "status": "1", "card_no": []string{"4242424242424242", "5555555555554444", "4000000000000002"}[index-1], "cvv": "123", "expire_at_str": "12/99", "pid": 267, "remark": f.remark}), nil
		}
		return transport(ctx, r)
	}
	for repeat := 0; repeat < 2; repeat++ {
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"open","count":3,"template_id":"template","request_id":"nex-batch-count1","confirm":true}`)).WithContext(ctx)
		w := httptest.NewRecorder()
		s.runNexovcc(w, r)
		cancel()
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		s.nexovcc.wg.Wait()
		if f.opens.Load() != 3 || s.nexovcc.job.CardDone != 3 || s.nexovcc.job.CardTotal != 3 || s.nexovcc.job.Status != "completed" {
			t.Fatal("wrong quantity or repeated issuance", f.opens.Load(), s.nexovcc.job)
		}
	}
	for _, payload := range []string{
		`{"action":"open","count":4,"template_id":"template","request_id":"nex-batch-count1","confirm":true}`,
		`{"action":"open","count":3,"template_id":"different","request_id":"nex-batch-count1","confirm":true}`,
	} {
		w := httptest.NewRecorder()
		s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(payload)))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		s.nexovcc.wg.Wait()
		if f.opens.Load() != 3 || s.nexovcc.job.Status != "failed" {
			t.Fatal("changed batch intent issued extra cards", f.opens.Load(), s.nexovcc.job)
		}
	}
}

func TestNexovccBatchOpenUnknownStopsFollowingCards(t *testing.T) {
	s, f := nexFixtureServer(t)
	f.noSerial = true
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"open","count":4,"template_id":"template","request_id":"nex-batch-unknown1","confirm":true}`)))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.nexovcc.wg.Wait()
	if f.opens.Load() != 1 || s.nexovcc.job.Status != "attention" || s.nexovcc.job.CardDone != 0 || s.nexovcc.job.CardTotal != 4 {
		t.Fatal(f.opens.Load(), s.nexovcc.job)
	}
}

func TestNexovccBatchOpenRejectsInvalidCount(t *testing.T) {
	s, f := nexFixtureServer(t)
	for _, count := range []string{"-1", "101", "1.5", `"3"`} {
		w := httptest.NewRecorder()
		s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"action":"open","count":%s,"template_id":"template","request_id":"nex-batch-invalid","confirm":true}`, count))))
		if w.Code != 400 || f.opens.Load() != 0 {
			t.Fatal(count, w.Code, f.opens.Load())
		}
	}
}

func TestNexovccBatchOpenStopWhileSubmitted(t *testing.T) {
	s, f := nexFixtureServer(t)
	original := s.nexovcc.transport
	entered := make(chan struct{})
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path != "/api/card/createUserCard" {
			return original(ctx, r)
		}
		f.opens.Add(1)
		close(entered)
		<-ctx.Done()
		return nexovcc.Response{}, ctx.Err()
	}
	w := payCall(t, s.runNexovcc, "", "", map[string]any{"action": "open", "count": 3, "template_id": "template", "request_id": "nex-batch-stop1", "confirm": true})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("issuance did not start")
	}
	if r := payCall(t, s.controlNexovcc, "", "", map[string]any{"action": "stop"}); r.Code != 200 {
		t.Fatal(r.Code)
	}
	s.nexovcc.wg.Wait()
	if f.opens.Load() != 1 || s.nexovcc.job.Status != "attention" || s.nexovcc.job.Operation == nil || s.nexovcc.job.Operation.Status != "unknown" {
		t.Fatal("stop lost submitted issuance or continued the batch", f.opens.Load(), s.nexovcc.job)
	}
}
