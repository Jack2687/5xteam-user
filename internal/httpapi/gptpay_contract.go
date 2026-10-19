package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"fmt"
)

// Called under the account lock, before acquiring or issuing a payment card.
func (s *Server) validateGPTPayNewPurchase(email string, cfg gptpay.Settings) error {
	if err := gptpay.ValidatePurchase(cfg); err != nil {
		return err
	}
	if cfg.Provider != gptpay.CMSNav {
		return nil
	}
	orders, _, err := s.store.GPTPayOrders(1, 0, email)
	if err != nil {
		return fmt.Errorf("读取原付款订单失败，未重新付款")
	}
	if len(orders) > 0 && orders[0].RetryBlocked() {
		return fmt.Errorf("CMSNav 原订单不允许重试，请先查询原订单并按供应商提示处理：%s", orders[0].Remote.RetryHint)
	}
	return nil
}
