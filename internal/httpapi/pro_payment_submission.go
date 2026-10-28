package httpapi

import (
	"context"
	"errors"
	"fmt"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

// The worker owns the account lock throughout this call. The stop/start gate
// serializes each admission with stopping; it is never held across network I/O.
func (s *Server) checkProAutoPayment(ctx context.Context, email string, expected model.ProAutoState, orderID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, _, err := s.store.MailAccountCredential(email)
	if err != nil {
		return err
	}
	a := p.ProAuto
	if p.ManagementScope != "pro" || a.ID == "" || a.ID != expected.ID || a.StopVersion != expected.StopVersion || a.StopRequested || a.Status != "running" || a.CardID != expected.CardID || a.PaymentOrderID() != expected.PaymentOrderID() || a.OrderID != orderID {
		return errors.New("账号流程已停止或变化，未提交开通订单")
	}
	return ctx.Err()
}

func (s *Server) submitProAutoPayment(ctx context.Context, expected model.ProAutoState, order gptpay.Order, snapshot gptpay.Snapshot) (result gptpay.Order, err error) {
	result = order
	if s.proAutoHooks != nil && s.proAutoHooks.BeforePaymentReserve != nil {
		s.proAutoHooks.BeforePaymentReserve()
	}
	s.proAutoMu.Lock()
	err = s.checkProAutoPayment(ctx, order.Email, expected, "")
	if err == nil {
		err = s.store.CreateGPTPayOrder(order, snapshot)
	}
	s.proAutoMu.Unlock()
	if err != nil {
		return result, err
	}
	// Only this invocation knows it created the order and has not entered the
	// supplier call. Missing remote IDs on historical orders are NOT proof.
	submissionStarted := false
	defer func() {
		if submissionStarted {
			return
		}
		cancelled, cleanupErr := s.store.FailUnsubmittedGPTPayOrder(order)
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("未提交订单清理失败，已保留占用待核实：%w", cleanupErr))
			return
		}
		result = cancelled
		s.auditGPTPayOrder(cancelled)
	}()
	if s.proAutoHooks != nil && s.proAutoHooks.PaymentReserved != nil {
		s.proAutoHooks.PaymentReserved()
	}
	s.proAutoMu.Lock()
	// Stop may have won while the local order was being prepared. Validate
	// again before attaching it or admitting a supplier request.
	err = s.checkProAutoPayment(ctx, order.Email, expected, "")
	if err == nil {
		_, err = s.store.UpdateProAccount(order.Email, func(p *model.MailAccountProfile) { p.ProAuto.OrderID = order.ID })
	}
	if err == nil {
		err = s.autoProUpdate(order.Email, "recharge", "running", "提交供应商开通订单，原登录会话保持中")
	}
	s.proAutoMu.Unlock()
	if err != nil {
		return result, err
	}
	s.queueNotification("pro_start", "Pro 全自动开始开通", "账号："+order.Email+"\n套餐："+order.PlanCode+"\n已完成登录，开始提交开通订单"+s.proCardProviderNotice(order.CardID))
	return s.submitGPTPayOrderWithAdmission(ctx, order, snapshot, func() error {
		if s.proAutoHooks != nil && s.proAutoHooks.BeforePaymentSubmit != nil {
			s.proAutoHooks.BeforePaymentSubmit()
		}
		s.proAutoMu.Lock()
		defer s.proAutoMu.Unlock()
		if err := s.checkProAutoPayment(ctx, order.Email, expected, order.ID); err != nil {
			return err
		}
		// The very next action is the supplier call. Once admitted, a stop or
		// transport failure can be ambiguous and must retain payment occupancy.
		submissionStarted = true
		return nil
	})
}
