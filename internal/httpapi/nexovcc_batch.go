package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// A single server job owns the whole selection, including when the browser
// changes pages. Each card still has its own durable, idempotent operation.
func (s *Server) batchNexovccLocked(ctx context.Context, in nexovccInput) {
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range in.CardIDs {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	s.nexovcc.jobMu.Lock()
	s.nexovcc.job.CardTotal = len(ids)
	s.nexovcc.jobMu.Unlock()
	success, pending, failed, skipped, done := 0, 0, 0, 0, 0
	lastError := ""
	for index, id := range ids {
		if ctx.Err() != nil {
			lastError = "任务已暂停，已提交操作保留在操作记录"
			break
		}
		if index > 0 {
			if err := s.nexovccBatchGap(ctx, time.Now()); err != nil {
				lastError = err.Error()
				break
			}
		}
		card, err := s.store.NexovccCard(id)
		s.nexovcc.jobMu.Lock()
		s.nexovcc.job.CardLabel = fmt.Sprintf("第 %d/%d 张 · 尾号 %s", index+1, len(ids), card.Last4)
		s.nexovcc.jobMu.Unlock()
		if err != nil {
			failed++
			done++
			lastError = "卡片不存在"
			continue
		}
		if card.Status == "4" {
			skipped++
			done++
			continue
		}
		hash := sha256.Sum256([]byte(in.RequestID + "|" + id))
		op, err := s.actionNexovccLocked(ctx, nexovccInput{Action: in.Action, CardID: id, RequestID: fmt.Sprintf("nex-%x", hash[:12])})
		done++
		switch {
		case err != nil:
			failed++
			lastError = err.Error()
		case op.Status == "failed":
			failed++
			lastError = op.Error
		case op.Status == "succeeded" && op.Action == "withdraw_all" && op.Amount == "0.00":
			skipped++
		case op.Status == "succeeded":
			success++
		default:
			pending++
		}
		s.nexovcc.jobMu.Lock()
		s.nexovcc.job.CardDone = done
		s.nexovcc.jobMu.Unlock()
		if op.Status == "unknown" {
			lastError = "本卡结果未知，已停止后续提交，请核实原操作"
			break
		}
	}
	s.nexovcc.jobMu.Lock()
	defer s.nexovcc.jobMu.Unlock()
	s.nexovcc.job.CardDone = done
	s.nexovcc.job.Status = "completed"
	if failed > 0 || pending > 0 || done < len(ids) {
		s.nexovcc.job.Status = "attention"
	}
	s.nexovcc.job.Message = fmt.Sprintf("已处理 %d/%d 张 · 成功 %d · 待同步 %d · 跳过 %d · 失败 %d", done, len(ids), success, pending, skipped, failed)
	if lastError != "" {
		s.nexovcc.job.Message += "；" + lastError
	}
}
