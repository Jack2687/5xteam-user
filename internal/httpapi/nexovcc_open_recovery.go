package httpapi

import (
	"context"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Reconcile an uncertain issuance by its unique remark and product. Absence
// from a list is not proof of failure and never permits replaying the write.
func (s *Server) findNexovccOpenSerialLocked(ctx context.Context, op nexovcc.Operation) (string, error) {
	var cached struct {
		At    time.Time `json:"at"`
		Error string    `json:"error"`
	}
	cacheKey := "open_query_" + op.ID
	if err := s.store.NexovccCached(cacheKey, &cached); err != nil {
		return "", err
	}
	if cached.Error != "" && time.Since(cached.At) < 30*time.Second && !nexovcc.ImmediateRequests(ctx) {
		return "", fmt.Errorf("%s；请稍后再查询", cached.Error)
	}
	c, _, err := s.nexovccClientLocked()
	if err != nil {
		return "", err
	}
	rows, err := c.Pages(ctx, "/api/card/findUserCardList")
	if err != nil {
		return "", fmt.Errorf("查询原开卡结果失败，未重新开卡：%w", err)
	}
	serial := ""
	for _, row := range rows {
		card, err := nexovcc.CardFrom(row)
		if err != nil {
			return "", err
		}
		if op.Remark == "" || card.Remark != op.Remark || card.ProductID != op.ProductID {
			continue
		}
		if serial != "" && serial != card.Serial {
			return "", fmt.Errorf("原开卡编号匹配到多张卡，请在环球卡台操作记录核实；未重新开卡")
		}
		serial = card.Serial
	}
	if serial == "" {
		cached.At = time.Now()
		cached.Error = "原开卡请求尚未查到对应卡片，未重新开卡；请到环球卡台 → 操作记录查询或确认原请求未执行、未扣款"
		if err := s.store.NexovccCache(cacheKey, cached); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%s", cached.Error)
	}
	return serial, nil
}

// A manual retry of a failed restock first recovers an already issued card.
// This path only queries; it cannot issue a second card or clear uncertainty.
func (s *Server) recoverNexovccOpenForProRetry(ctx context.Context) error {
	if err := s.lockNexovccOperation(ctx); err != nil {
		return err
	}
	defer s.nexovcc.mu.Unlock()
	ops, err := s.store.NexovccOperations()
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.Action != "open" || op.Terminal() || op.Serial != "" {
			continue
		}
		if _, err = s.resolveNexovccLocked(ctx, nexovccInput{RequestID: op.ID, Outcome: "query"}); err != nil {
			return err
		}
	}
	return nil
}
