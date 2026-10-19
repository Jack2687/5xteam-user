package httpapi

import (
	"context"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

// Query only: never replay card.cancel.info, including after timeouts/restarts.
func (s *Server) checkEJHCancellationLocked(ctx context.Context, cfg ejh.Settings, key string, op ejh.Operation) (ejh.Operation, error) {
	var remote ejh.Card
	err := s.ejhClientLocked().Call(ctx, cfg, key, "card.query.info", map[string]string{"orderNo": op.OrderNo}, &remote)
	if err == nil && remote.OrderNo != op.OrderNo {
		err = fmt.Errorf("卡台返回订单号不一致，保留原销卡状态")
	}
	if err == nil {
		_, err = s.store.ImportEJHCard(remote, false)
	}
	var saved ejh.Operation
	if readErr := s.store.EJHGet("operation", op.ID, &saved); readErr != nil {
		return op, readErr
	}
	if saved.Status == "completed" || saved.Status == "failed" || saved.Status == "resolved" {
		return saved, nil
	}
	saved.CheckError = ""
	if err != nil {
		saved.CheckError = err.Error()
	}
	next := time.Now().Add(5 * time.Second)
	if saved.Status == "unknown" && time.Since(saved.CreatedAt) > 5*time.Minute {
		next = time.Now().Add(time.Minute)
	}
	saved.NextCheckAt, saved.UpdatedAt = &next, time.Now()
	if err = s.store.EJHPut("operation", saved.ID, saved); err != nil {
		return saved, err
	}
	return saved, nil
}

func (s *Server) tickEJHCancellations(ctx context.Context) {
	if !s.ejh.mu.TryLock() {
		return
	}
	defer s.ejh.mu.Unlock()
	cfg, key, err := s.ejhConfig()
	if err != nil {
		return
	}
	ops, err := s.store.EJHPendingCancellations(time.Now(), 10)
	if err != nil || len(ops) == 0 {
		return
	}
	job, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, op := range ops {
		if job.Err() != nil {
			return
		}
		_, _ = s.checkEJHCancellationLocked(job, cfg, key, op)
	}
}
