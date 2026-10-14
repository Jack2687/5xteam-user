package httpapi

import (
	"chapt-space-user/internal/ejh"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) syncEJH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	end := time.Now()
	start := end.AddDate(0, 0, -7)
	var e error
	zone := time.FixedZone("CST", 8*3600)
	if in.Start != "" {
		start, e = time.ParseInLocation("2006-01-02", in.Start, zone)
		if e != nil {
			writeAPI(w, 400, nil, "开始日期无效")
			return
		}
	}
	if in.End != "" {
		end, e = time.ParseInLocation("2006-01-02", in.End, zone)
		end = end.Add(24*time.Hour - time.Second)
		if e != nil {
			writeAPI(w, 400, nil, "结束日期无效")
			return
		}
	}
	if !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		writeAPI(w, 400, nil, "查询范围须为 1～366 天，后台按 90 天分段")
		return
	}
	if !s.ejh.mu.TryLock() {
		writeAPI(w, 409, nil, "卡台正在同步或执行操作，请稍后查看记录")
		return
	}
	defer s.ejh.mu.Unlock()
	e = s.syncEJHLocked(r.Context(), start, end)
	if e != nil {
		writeAPI(w, 502, nil, e.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"synced": true}, "")
}
func (s *Server) syncEJHLocked(ctx context.Context, start, end time.Time) (retErr error) {
	var state ejh.SyncState
	_ = s.store.EJHGet("sync", "latest", &state)
	now := time.Now()
	state.LastAttempt = &now
	defer func() {
		if retErr != nil {
			state.Error = retErr.Error()
		} else {
			state.LastSuccess = &now
			state.Error = ""
		}
		if e := s.store.EJHPut("sync", "latest", state); e != nil && retErr == nil {
			retErr = e
		}
	}()
	cfg, key, e := s.ejhConfig()
	if e != nil {
		return e
	}
	client := s.ejhClientLocked()
	seenCards := map[[32]byte]bool{}
	cardErrors := 0
	for page := 1; page <= 10000; page++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		var out struct {
			Cards []ejh.Card `json:"cardInfos"`
		}
		if e = client.Call(ctx, cfg, key, "card.list.info", map[string]any{"page": page, "size": 100}, &out); e != nil {
			return e
		}
		for _, remote := range out.Cards {
			if _, e = s.store.ImportEJHCard(remote, false); e != nil {
				cardErrors++
				var rec ejh.CardRecord
				if s.store.EJHGet("card", ejhCardID(remote.OrderNo), &rec) == nil {
					rec.Error = e.Error()
					if saveErr := s.store.EJHPut("card", rec.ID, rec); saveErr != nil {
						return saveErr
					}
				}
			}
		}
		raw, _ := json.Marshal(out.Cards)
		hash := sha256.Sum256(raw)
		if len(out.Cards) > 0 && seenCards[hash] {
			return fmt.Errorf("卡片接口重复返回同一页，已停止同步")
		}
		seenCards[hash] = true
		if len(out.Cards) < 100 {
			break
		}
		if page == 10000 {
			return fmt.Errorf("卡片分页未结束，请检查供应商返回")
		}
	}
	zone := time.FixedZone("CST", 8*3600)
	for from := start; from.Before(end); {
		to := from.Add(89 * 24 * time.Hour)
		if to.After(end) {
			to = end
		}
		seenTrades := map[[32]byte]bool{}
		for page := 1; page <= 10000; page++ {
			if e = ctx.Err(); e != nil {
				return e
			}
			var out struct {
				Trades []ejh.Trade `json:"tradeInfos"`
			}
			if e = client.Call(ctx, cfg, key, "card.trade.info", map[string]any{"tradeTime": from.In(zone).Format("2006-01-02 15:04:05"), "endTime": to.In(zone).Format("2006-01-02 15:04:05"), "page": page, "size": 100}, &out); e != nil {
				return e
			}
			for _, t := range out.Trades {
				if e = s.store.EJHSaveTrade(ejhCardID(t.OrderNo), t); e != nil {
					return e
				}
			}
			raw, _ := json.Marshal(out.Trades)
			hash := sha256.Sum256(raw)
			if len(out.Trades) > 0 && seenTrades[hash] {
				return fmt.Errorf("交易接口重复返回同一页，已停止同步")
			}
			seenTrades[hash] = true
			if len(out.Trades) < 100 {
				break
			}
			if page == 10000 {
				return fmt.Errorf("交易分页未结束，请缩小时间范围")
			}
		}
		from = to
	}
	if cardErrors > 0 {
		return fmt.Errorf("流水已同步；%d 张卡片资料不完整，未加入 Pro，请在卡片页面刷新详情", cardErrors)
	}
	return nil
}
func (s *Server) startEJH(parent context.Context) {
	s.ejh.lifecycle.Lock()
	if s.ejh.cancel != nil {
		s.ejh.lifecycle.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.ejh.cancel = cancel
	s.ejh.wg.Add(2)
	s.ejh.lifecycle.Unlock()
	go func() {
		defer s.ejh.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickProForceMerge(ctx)
			}
		}
	}()
	go func() {
		defer s.ejh.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		next := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Now().Before(next) {
					continue
				}
				cfg, _, e := s.store.EJHSettings()
				if e != nil || !cfg.Enabled || !cfg.SyncEnabled {
					next = time.Now().Add(time.Minute)
					continue
				}
				if !s.ejh.mu.TryLock() {
					continue
				}
				job, done := context.WithTimeout(ctx, 90*time.Second)
				_ = s.syncEJHLocked(job, time.Now().AddDate(0, 0, -90), time.Now())
				done()
				s.ejh.mu.Unlock()
				next = time.Now().Add(time.Duration(cfg.SyncIntervalSeconds) * time.Second)
			}
		}
	}()
}
func (s *Server) stopEJH() {
	s.ejh.lifecycle.Lock()
	if s.ejh.cancel != nil {
		s.ejh.cancel()
	}
	s.ejh.lifecycle.Unlock()
	s.ejh.wg.Wait()
}
