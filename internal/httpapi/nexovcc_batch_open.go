package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// Keep the whole requested count under one job so periodic sync cannot replace
// its progress between cards. Each issuance retains a stable operation ID.
func (s *Server) batchOpenNexovccLocked(ctx context.Context, in nexovccInput) {
	// Remember the intent before any charge. Reusing an ID with another count
	// or template must never turn a retry into an additional purchase.
	var prior struct {
		Count    int    `json:"count"`
		Template string `json:"template"`
	}
	key := "batch-open:" + in.RequestID
	err := s.store.NexovccCached(key, &prior)
	if err == nil && prior.Count != 0 && (prior.Count != in.Count || prior.Template != in.TemplateID) {
		err = fmt.Errorf("原批次的数量或模板不一致，未重复开卡")
	}
	if err == nil && prior.Count == 0 {
		prior.Count, prior.Template = in.Count, in.TemplateID
		err = s.store.NexovccCache(key, prior)
	}
	if err != nil {
		s.nexovcc.jobMu.Lock()
		s.nexovcc.job.Status, s.nexovcc.job.Message = "failed", err.Error()
		s.nexovcc.jobMu.Unlock()
		return
	}
	s.nexovcc.jobMu.Lock()
	s.nexovcc.job.CardTotal = in.Count
	s.nexovcc.jobMu.Unlock()
	done, message := 0, ""
	for i := 0; i < in.Count; i++ {
		if ctx.Err() != nil {
			message = "开卡已停止，已提交记录保留"
			break
		}
		if i > 0 {
			if err := s.nexovccBatchGap(ctx, time.Now()); err != nil {
				message = err.Error()
				break
			}
		}
		s.nexovcc.jobMu.Lock()
		s.nexovcc.job.CardLabel = fmt.Sprintf("开卡 %d/%d", i+1, in.Count)
		s.nexovcc.job.Message = "正在开卡，完成后自动继续下一张"
		s.nexovcc.jobMu.Unlock()
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s|open|%d", in.RequestID, i)))
		op, err := s.openNexovccLocked(ctx, fmt.Sprintf("nex-%x", hash[:12]), in.TemplateID, false)
		if err == nil && op.Status == "import_pending" && op.Serial != "" {
			s.nexovccProgress("本卡已开出，等待资料同步后继续下一张…", 0, 1)
			op, err = s.waitNexovccImportLocked(ctx, op, 2*time.Minute, 3*time.Second)
		}
		s.nexovcc.jobMu.Lock()
		if op.ID != "" {
			s.nexovcc.job.Operation = &op
		}
		s.nexovcc.jobMu.Unlock()
		if err != nil || op.Status != "succeeded" {
			message = firstNonEmpty(op.Error, "本卡结果未确认，已停止后续开卡")
			if err != nil {
				message = err.Error()
			}
			break
		}
		done++
		s.nexovcc.jobMu.Lock()
		s.nexovcc.job.CardDone = done
		s.nexovcc.jobMu.Unlock()
	}
	s.nexovcc.jobMu.Lock()
	defer s.nexovcc.jobMu.Unlock()
	s.nexovcc.job.Status = "completed"
	s.nexovcc.job.Message = fmt.Sprintf("开卡成功 %d/%d 张", done, in.Count)
	if done < in.Count {
		s.nexovcc.job.Status = "attention"
		s.nexovcc.job.Message += "；批次已暂停，请查看本卡操作记录。" + message
	}
}
