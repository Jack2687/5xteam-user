package httpapi

import (
	"context"
	"fmt"

	"chatgpt-space-merge/internal/nexovcc"
)

// Only an explicit copy requests missing holder details. Do not add balance,
// transaction or inventory queries, or change background synchronization scope.
func (s *Server) nexovccCardHolder(ctx context.Context, id string) (*nexovcc.CardHolder, error) {
	if !s.nexovcc.mu.TryLock() {
		return nil, fmt.Errorf("环球正在执行其他操作，持卡人资料尚未保存，请稍后重试复制")
	}
	defer s.nexovcc.mu.Unlock()
	card, err := s.store.NexovccCard(id)
	if err != nil {
		return nil, err
	}
	if card.Holder != nil || card.Status == "4" {
		return card.Holder, nil
	}
	ctx, finish, err := s.nexovccOperationContext(nexovcc.WithImmediateRequests(ctx))
	if err != nil {
		return nil, err
	}
	defer finish()
	client, _, err := s.nexovccClientLocked()
	if err != nil {
		return nil, err
	}
	data, err := client.Do(ctx, "/api/card/getUserCardDetail", map[string]any{"serial_no": card.Serial})
	if err != nil {
		return nil, err
	}
	detail, err := nexovcc.Object(data)
	if err != nil || nexovcc.String(detail["serial_no"]) != card.Serial {
		return nil, fmt.Errorf("环球未返回当前卡片的详情，请稍后重试复制")
	}
	holder := nexovcc.CardHolderFrom(detail)
	if holder == nil {
		return nil, fmt.Errorf("环球当前未返回该卡持卡人信息，请稍后重试复制")
	}
	if err = s.store.SaveNexovccCardHolder(card.ID, holder); err != nil {
		return nil, fmt.Errorf("保存持卡人资料失败")
	}
	return holder, nil
}
