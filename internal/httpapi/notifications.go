package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/notify"
	"chatgpt-space-merge/internal/store"
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"
)

type notificationEvent struct {
	Kind, Title, Text string
	At                time.Time
}
type notificationRuntime struct {
	queue  chan notificationEvent
	cancel context.CancelFunc
	wg     sync.WaitGroup
	client *http.Client
}

func (s *Server) startNotifications() {
	ctx, cancel := context.WithCancel(context.Background())
	s.notifications.cancel = cancel
	s.notifications.queue = make(chan notificationEvent, 256)
	s.notifications.wg.Add(1)
	go func() {
		defer s.notifications.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-s.notifications.queue:
				s.deliverNotification(ctx, event)
			}
		}
	}()
}
func (s *Server) stopNotifications() {
	if s.notifications.cancel != nil {
		s.notifications.cancel()
	}
	s.notifications.wg.Wait()
}
func (s *Server) queueNotification(kind, title, text string) {
	if s.notifications.queue == nil {
		return
	}
	select {
	case s.notifications.queue <- notificationEvent{Kind: kind, Title: title, Text: text, At: time.Now()}:
	default:
		slog.Warn("notification queue full", "event", kind)
	}
}
func (s *Server) deliverNotification(ctx context.Context, event notificationEvent) {
	settings, err := s.store.NotificationSettings()
	if err != nil {
		slog.Error("read notification settings failed")
		return
	}
	if !settings.Accepts(event.Kind) {
		return
	}
	for _, bot := range settings.Bots {
		if !settings.AcceptsBot(bot, event.Kind) {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		text := "Space · " + event.Title + "\n" + event.Text + "\n时间：" + event.At.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05")
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := notify.Send(requestCtx, s.notifications.client, bot, text)
		cancel()
		entry := store.NotificationLog{At: time.Now(), Bot: bot.Name, Kind: event.Kind, Title: event.Title, Status: "sent"}
		if err != nil {
			entry.Status = "failed"
			entry.Error = err.Error()
		}
		if err := s.store.RecordNotification(entry); err != nil {
			slog.Error("save notification result failed")
		}
	}
}

func (s *Server) notifyProAutomation(p model.MailAccountProfile, stage, status string) {
	text := "账号：" + p.Email + "\n套餐：" + p.ProAuto.PlanCode + s.proCardProviderNotice(p.ProAuto.CardID)
	if stage == "push" && status == "completed" {
		s.queueNotification("pro_pushed", "Pro 全自动推送完成", text+"\n已开通并完成推送，可在 Pro 管理查看")
		return
	}
	label := map[string]string{"login": "登录", "recharge": "开通", "oauth": "获取 RT/AT", "push": "推送"}[stage]
	text += "\n失败位置：" + label
	title := "Pro 全自动失败"
	if stage == "recharge" && p.ProAuto.OrderID != "" {
		if order, _, err := s.store.GPTPayOrder(p.ProAuto.OrderID); err == nil && order.Status != "failed" && order.Status != "success" {
			title = "Pro 开通结果待确认"
			text += "\n原订单已保留，请先核实付款结果"
		}
	}
	// Error details can contain session credentials or upstream responses.
	s.queueNotification("pro_failed", title, text+"\n请在 Pro 管理查看失败详情和重试状态")
}
func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.NotificationSettings()
	if err != nil {
		writeAPI(w, 500, nil, "读取通知配置失败")
		return
	}
	logs, err := s.store.NotificationLogs()
	if err != nil {
		writeAPI(w, 500, nil, "读取通知记录失败")
		return
	}
	writeAPI(w, 200, map[string]any{"settings": cfg.Public(), "logs": logs}, "")
}
func (s *Server) saveNotifications(w http.ResponseWriter, r *http.Request) {
	var in notify.Settings
	if decodeJSON(w, r, &in, 64<<10) != nil {
		return
	}
	out, err := s.store.SaveNotificationSettings(in)
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, out, "")
}
func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	var in notify.Bot
	if decodeJSON(w, r, &in, 8<<10) != nil {
		return
	}
	cfg, err := s.store.NotificationSettings()
	if err != nil {
		writeAPI(w, 500, nil, "读取通知配置失败")
		return
	}
	merged, err := notify.MergeSettings(notify.Settings{Bots: []notify.Bot{in}}, cfg)
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	bot := merged.Bots[0]
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err = notify.Send(ctx, s.notifications.client, bot, "Space · 测试通知\n飞书机器人连接成功。")
	entry := store.NotificationLog{At: time.Now(), Bot: bot.Name, Kind: "test", Title: "测试通知", Status: "sent"}
	if err != nil {
		entry.Status = "failed"
		entry.Error = err.Error()
	}
	_ = s.store.RecordNotification(entry)
	if err != nil {
		writeAPI(w, 502, nil, err.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"sent": true}, "")
}

type mergeNoticeKey struct{}
type mergeNoticeState struct{ failed, removed bool }

func (s *Server) notifyMergeStarted(p model.MailAccountProfile) {
	standard, user := "未查询", "未查询"
	settings, _, _, err := s.store.ProSettings()
	if err == nil && proCostBindingMatches(p, settings) && p.CostCheckedAt != nil {
		standard = notificationUSD(p.CostDownstreamSnapshot)
		if p.UserCostDownstreamIdentity == p.CostDownstreamIdentity {
			user = notificationUSD(p.UserCostDownstreamSnapshot)
		}
	}
	text := "账号：" + p.Email + "\n消耗额度（标准）：" + standard + "\n用户费用（已用）：" + user + "\n已进入合并流程，按配置并发执行"
	s.queueNotification("merge_start", "Pro 触发合并", text+s.proCardProviderNotice(p.ProAuto.CardID))
}

func notificationUSD(value float64) string {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "未查询"
	}
	return fmt.Sprintf("$%.4f", value)
}

func (s *Server) notifyEJHOpened(op ejh.Operation) {
	if op.Method != "open" {
		return
	}
	source := "手动开卡"
	if op.Automatic {
		source = "自动开卡"
	}
	title, result := "EJH 开卡待核实", "响应未确认，请在 EJH 操作记录核实，勿重复开卡"
	switch op.Status {
	case "completed":
		title, result = "EJH 开卡成功", "卡片已加入卡片管理"
	case "failed":
		title, result = "EJH 开卡失败", "请在 EJH 操作记录查看失败详情"
	case "import_pending":
		title, result = "EJH 开卡待导入", "卡台已返回卡片，本地导入未完成；请核实原操作，勿重复开卡"
	}
	text := fmt.Sprintf("来源：%s\n开卡金额：%s %s\n操作编号：%s", source, op.Amount, op.Currency, op.ID)
	if op.CardID != "" {
		if card, _, err := s.store.GPTPayCard(op.CardID); err == nil && len(card.Last4) == 4 {
			text += "\n卡片尾号：" + card.Last4
		}
	}
	s.queueNotification("ejh_open", title, text+"\n"+result)
}

func (s *Server) notifyMergeFailure(ctx context.Context, p model.MailAccountProfile, stage string, retrying, uncertain bool) {
	if state, ok := ctx.Value(mergeNoticeKey{}).(*mergeNoticeState); ok {
		state.failed = true
	}
	label := proStageLabel(stage)
	if label == "" {
		label = "流程准备或下游处理"
	}
	text := fmt.Sprintf("账号：%s\n失败位置：%s\n请在 Pro 管理查看失败详情", p.Email, label)
	if retrying {
		text += "\n将按配置自动重试"
	} else {
		text += "\n本次执行已停止"
	}
	title := "Pro 合并失败"
	if uncertain {
		title = "Pro 合并结果待确认"
		text += "\n请求响应未确认，请先核实远端结果，避免重复合并"
	}
	s.queueNotification("merge_result", title, text+s.proCardProviderNotice(p.ProAuto.CardID))
}
func (s *Server) notifyMergeRemoved(ctx context.Context, p model.MailAccountProfile) {
	if state, ok := ctx.Value(mergeNoticeKey{}).(*mergeNoticeState); ok {
		if state.removed {
			return
		}
		state.removed = true
	}
	s.queueNotification("merge_result", "Pro 合并成功移出", "账号："+p.Email+"\n空间合并完成，已成功移出母号"+s.proCardProviderNotice(p.ProAuto.CardID))
}

func (s *Server) proCardProviderNotice(id string) string {
	if id != "" {
		if card, _, e := s.store.GPTPayCard(id); e == nil {
			if card.Source == "nexovcc" {
				return "\n卡台：环球"
			}
			if card.Source == "ejh" {
				return "\n卡台：EJH"
			}
		}
	}
	return ""
}
