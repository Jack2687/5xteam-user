package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

// A durable claimed task outlives the evaluate HTTP request. Shutdown still
// cancels and joins preparation; user stops are checked by card admission.
func (s *Server) beginRestockStart(parent context.Context, id string) (context.Context, func(), error) {
	s.proAutoMu.Lock()
	defer s.proAutoMu.Unlock()
	if s.proAutoClosed || s.proRestockStartJobs[id] != nil {
		return nil, nil, fmt.Errorf("补货准备已在执行或服务正在停止")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Minute)
	if s.proRestockStartJobs == nil {
		s.proRestockStartJobs = map[string]context.CancelFunc{}
	}
	s.proRestockStartJobs[id] = cancel
	s.proAutoWG.Add(1)
	return ctx, func() {
		cancel()
		s.proAutoMu.Lock()
		delete(s.proRestockStartJobs, id)
		s.proAutoMu.Unlock()
		s.proAutoWG.Done()
	}, nil
}

func (s *Server) trackRestockCardImport(t *restock.Task, p model.MailAccountProfile) (bool, error) {
	if t.CardProvider != "nexovcc" || t.Status == "reserved" || p.ProRestockTaskID != t.ID || p.ProAuto.ID != "" || p.PushStatus == "completed" || p.DispositionStarted() || p.SpaceMergedOnce {
		return false, nil
	}
	op, err := s.store.NexovccRestockOperation(t.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if op.Serial == "" || (op.Status != "import_pending" && op.Status != "succeeded") {
		return false, nil
	}
	t.CardID, t.Status, t.Stage, t.ETA = op.CardID, "waiting_card", "card", nil
	if t.NextCardCheckAt == nil {
		next := time.Now().Add(30 * time.Second)
		t.NextCardCheckAt = &next
	}
	t.UpdatedAt = time.Now()
	t.Message = "环球原卡已开出，等待资料同步；完成后自动续跑原账号"
	if op.Status == "succeeded" {
		t.Message = "环球原卡资料已齐，等待续跑原账号"
	}
	if p.ProAuto.StopRequested || p.ProAuto.Status == "stopped" {
		t.Status, t.Message = "attention", "用户已停止，原卡与补货记录保留；不会自动续跑"
	}
	return true, nil
}

func (s *Server) startClaimedProRestock(ctx context.Context, t restock.Task, pay gptpay.Settings, cardID string) {
	body, _ := json.Marshal(map[string]any{"provider": pay.Provider, "plan_code": pay.PlanCode, "card_id": cardID, "country": pay.Country, "credit_quantity": pay.CreditQuantity})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(context.WithValue(ctx, proRestockContextKey{}, t.ID))
	r.SetPathValue("email", t.Email)
	w := httptest.NewRecorder()
	s.startProAutomation(w, r)
	if w.Code == http.StatusAccepted {
		return
	}
	t.Status, t.ETA = "attention", nil
	t.Message = "启动未获确认；保留原账号、预算与在途"
	var reply struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(w.Body.Bytes(), &reply) == nil && reply.Error != "" {
		t.Message = reply.Error
	}
	if w.Header().Get("X-Pro-Card-Wait") == "nexovcc" {
		t.Status, t.SubmittedAt = "reserved", nil
	} else if p, _, err := s.store.MailAccountCredential(t.Email); err == nil {
		_, _ = s.trackRestockCardImport(&t, p)
	}
	t.UpdatedAt = time.Now()
	_ = s.store.SaveProRestockTask(t)
}

// Only resumes work that already issued its own card. It never opens another
// card, resubmits an uncertain issuance, or makes whole-platform sync calls.
func (s *Server) continueProRestockCardImports(ctx context.Context, c restock.Config, p model.ProSettings) {
	if s.checkNexovccControl() != nil {
		return
	}
	if !c.Enabled || c.Mode != "auto" || ctx.Err() != nil {
		return
	}
	issuer, _, err := s.store.NexovccSettings()
	if err != nil || !issuer.Enabled {
		return
	}
	tasks, err := s.store.ProRestockTasks()
	if err != nil {
		return
	}
	pay, _, err := s.store.GPTPaySettings()
	if err != nil {
		return
	}
	for _, t := range tasks {
		if !t.Active() || t.Status == "reserved" || t.CardProvider != "nexovcc" || (t.NextCardCheckAt != nil && t.NextCardCheckAt.After(time.Now())) {
			continue
		}
		profile, _, err := s.store.MailAccountCredential(t.Email)
		if err != nil || profile.ProRestockTaskID != t.ID || profile.ProAuto.ID != "" || profile.ProAuto.StopRequested || profile.ProAuto.Status == "stopped" || profile.DispositionStarted() || profile.SpaceMergedOnce || profile.PushStatus == "completed" || profile.ProWorkflowRunning || s.proLoginAlreadyRunning(t.Email) {
			continue
		}
		s.proMergeMu.Lock()
		running := s.proAutoRunning
		s.proMergeMu.Unlock()
		s.proAutoMu.Lock()
		busy := s.proAutoClosed || max(running, len(s.proAutoJobs)) >= p.Concurrency
		s.proAutoMu.Unlock()
		if busy {
			return
		}
		op, err := s.store.NexovccRestockOperation(t.ID)
		if err != nil || op.Serial == "" || (op.Status != "import_pending" && op.Status != "succeeded") {
			continue
		}
		taskPay, err := applyPurchasePay(pay, t.PurchaseRoute)
		if err != nil {
			continue
		}
		if restockPlan(taskPay.Provider, taskPay.PlanCode, taskPay.Country, taskPay.CreditQuantity) != t.Plan {
			continue
		}
		next := time.Now().Add(time.Duration(max(30, p.RetryIntervalSeconds)) * time.Second)
		t.NextCardCheckAt, t.CardID, t.ETA = &next, op.CardID, nil
		if err = s.store.SaveProRestockTask(t); err != nil {
			return
		}
		workCtx, finish, err := s.beginRestockStart(ctx, t.ID)
		if err != nil {
			return
		}
		func() {
			defer finish()
			bound, finishIssuer, bindErr := s.nexovccOperationContext(workCtx)
			if bindErr != nil {
				return
			}
			defer finishIssuer()
			workCtx = bound
			if op.Status == "import_pending" {
				if err = s.lockNexovccOperation(workCtx); err != nil {
					return
				}
				// Re-read after acquiring the issuer lock: a page sync may have
				// finished the exact operation while this task was waiting.
				latest, readErr := s.store.NexovccOperation(op.ID)
				if readErr == nil {
					op = latest
				}
				if readErr == nil && op.Status == "import_pending" {
					card, cardErr := s.store.NexovccCard(op.CardID)
					if cardErr == nil && card.Imported && card.Error == "" {
						op.Status, op.Error = "succeeded", ""
					} else {
						op = s.verifyNexovccOperationLocked(workCtx, op)
					}
					readErr = s.store.SaveNexovccOperation(op)
				}
				s.nexovcc.mu.Unlock()
				if readErr != nil || op.Status != "succeeded" {
					return
				}
			}
			card, _, err := s.store.GPTPayCard(op.CardID)
			if err != nil || !card.Available || card.Source != "nexovcc" {
				return
			}
			s.startClaimedProRestock(workCtx, t, taskPay, op.CardID)
		}()
	}
}
