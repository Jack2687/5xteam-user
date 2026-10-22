package httpapi

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errNexovccReadYielded = errors.New("流水查询已暂停，优先处理卡片操作；已保存的数据保留")

// The caller holds nexovcc.mu. Only read-only work registers a cancellation
// handle. Submitted money operations are never interrupted for a new opening.
func (s *Server) nexovccReadContext(parent context.Context) (context.Context, func() bool) {
	ctx, cancel := context.WithCancel(parent)
	s.nexovcc.jobMu.Lock()
	s.nexovcc.readCancel = cancel
	s.nexovcc.job.ReadOnly = true
	s.nexovcc.jobMu.Unlock()
	var once sync.Once
	yielded := false
	return ctx, func() bool {
		once.Do(func() {
			s.nexovcc.jobMu.Lock()
			yielded = ctx.Err() != nil
			s.nexovcc.readCancel = nil
			s.nexovcc.job.ReadOnly = false
			cancel()
			s.nexovcc.jobMu.Unlock()
		})
		return yielded
	}
}

func (s *Server) yieldNexovccRead() bool {
	s.nexovcc.jobMu.Lock()
	defer s.nexovcc.jobMu.Unlock()
	if s.nexovcc.readCancel == nil {
		return false
	}
	s.nexovcc.readCancel()
	return true
}

func (s *Server) lockNexovccOperation(ctx context.Context) error {
	for !s.nexovcc.mu.TryLock() {
		if err := s.checkNexovccControl(); err != nil {
			return err
		}
		s.yieldNexovccRead()
		t := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	if err := s.checkNexovccControl(); err != nil {
		s.nexovcc.mu.Unlock()
		return err
	}
	return nil
}
