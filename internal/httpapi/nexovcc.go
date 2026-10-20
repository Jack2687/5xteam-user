package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

// Overlay only the general Pro views. EJH cost and reconciliation endpoints
// continue to use their existing EJH-only source.
type proFinancialCase struct {
	ejh.Case
	Nexovcc *store.NexovccCase `json:"nexovcc,omitempty"`
}

func (s *Server) proFinancialCases(cases []ejh.Case) ([]proFinancialCase, error) {
	out := make([]proFinancialCase, len(cases))
	needed := false
	for _, c := range cases {
		needed = needed || c.CardType == "nexovcc"
	}
	byID := map[string]store.NexovccCase{}
	if needed {
		rows, err := s.store.NexovccCases()
		if err != nil {
			return nil, err
		}
		for _, n := range rows {
			byID[n.ID] = n
		}
	}
	for i, c := range cases {
		out[i].Case = c
		if n, ok := byID[c.ID]; ok && c.CardType == "nexovcc" {
			out[i].Nexovcc = &n
			out[i].State = n.State
			out[i].RequestedAt = nil
			out[i].RequestSource = ""
			if at, err := time.Parse(time.RFC3339Nano, n.RequestedAt); err == nil {
				out[i].RequestedAt = &at
				if n.ManualState == "waiting" {
					out[i].RequestSource = "manual"
				}
			}
		}
	}
	return out, nil
}

type nexovccJob struct {
	ID        string             `json:"id"`
	Status    string             `json:"status"`
	Message   string             `json:"message"`
	Done      int                `json:"done"`
	Total     int                `json:"total"`
	Operation *nexovcc.Operation `json:"operation,omitempty"`
	CardDone  int                `json:"card_done,omitempty"`
	CardTotal int                `json:"card_total,omitempty"`
	CardLabel string             `json:"card_label,omitempty"`
}
type nexovccRuntime struct {
	mu            sync.Mutex
	client        *nexovcc.Client
	fingerprint   [32]byte
	transport     nexovcc.Transport
	jobMu         sync.Mutex
	job           nexovccJob
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	closed        bool
	monitorCancel context.CancelFunc
}
type nexovccInput struct {
	AutomaticRefresh bool     `json:"automatic_refresh,omitempty"`
	CapacityRecycle  bool     `json:"-"`
	AutoWithdraw     bool     `json:"-"`
	Action           string   `json:"action"`
	RequestID        string   `json:"request_id"`
	CardID           string   `json:"card_id"`
	CardIDs          []string `json:"card_ids,omitempty"`
	TemplateID       string   `json:"template_id"`
	Amount           string   `json:"amount"`
	Remark           string   `json:"remark"`
	Confirm          bool     `json:"confirm"`
	Serial           string   `json:"serial_no"`
	Outcome          string   `json:"outcome"`
	Evidence         string   `json:"evidence"`
}

func (s *Server) nexovccRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/nexovcc/settings", s.getNexovccSettings)
	m.HandleFunc("PUT /api/nexovcc/settings", s.saveNexovccSettings)
	m.HandleFunc("GET /api/nexovcc/dashboard", s.nexovccDashboard)
	m.HandleFunc("GET /api/nexovcc/occupancy/{id}", s.getEJHOccupancyOrder)
	m.HandleFunc("POST /api/nexovcc/occupancy/{id}/resolve", s.resolveNexovccOccupancy)
	m.HandleFunc("POST /api/nexovcc/run", s.runNexovcc)
	m.HandleFunc("GET /api/nexovcc/job", func(w http.ResponseWriter, r *http.Request) {
		s.nexovcc.jobMu.Lock()
		job := s.nexovcc.job
		s.nexovcc.jobMu.Unlock()
		writeAPI(w, 200, job, "")
	})
	m.HandleFunc("POST /api/nexovcc/cards/{id}/private", s.nexovccPrivate)
	m.HandleFunc("PUT /api/nexovcc/cases/{id}", s.nexovccCaseState)
	m.HandleFunc("DELETE /api/nexovcc/cases/unopened", s.deleteNexovccUnopenedCases)
	m.HandleFunc("PUT /api/nexovcc/cases/{id}/match", s.nexovccCaseMatch)
	m.HandleFunc("PUT /api/nexovcc/trades/{id}/purpose", s.nexovccTradePurpose)
	m.HandleFunc("PUT /api/nexovcc/trade-reviews/{id}", s.nexovccTradeReview)
}
func (s *Server) stopNexovcc() {
	s.nexovcc.jobMu.Lock()
	s.nexovcc.closed = true
	if s.nexovcc.monitorCancel != nil {
		s.nexovcc.monitorCancel()
	}
	if s.nexovcc.cancel != nil {
		s.nexovcc.cancel()
	}
	s.nexovcc.jobMu.Unlock()
	s.nexovcc.wg.Wait()
}
func (s *Server) getNexovccSettings(w http.ResponseWriter, r *http.Request) {
	cfg, _, e := s.store.NexovccSettings()
	if e != nil {
		writeAPI(w, 500, nil, "读取环球配置失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAPI(w, 200, cfg, "")
}
func (s *Server) saveNexovccSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		nexovcc.Settings
		nexovcc.Credentials
		ClearTOTP bool `json:"clear_totp"`
	}
	if decodeJSON(w, r, &input, 1<<20) != nil {
		return
	}
	if !s.nexovcc.mu.TryLock() {
		writeAPI(w, 409, nil, "环球正在执行操作，请完成后再保存配置")
		return
	}
	defer s.nexovcc.mu.Unlock()
	cfg, e := s.store.SaveNexovccSettings(input.Settings, input.Credentials, input.ClearTOTP)
	if e != nil {
		writeAPI(w, 400, nil, e.Error())
		return
	}
	writeAPI(w, 200, cfg, "")
}
func (s *Server) nexovccClientLocked() (*nexovcc.Client, nexovcc.Settings, error) {
	cfg, k, e := s.store.NexovccSettings()
	if e != nil {
		return nil, cfg, e
	}
	// The switch gates automatic work; manual management still uses saved credentials.
	if k.Login == "" || k.Password == "" {
		return nil, cfg, fmt.Errorf("请先保存环球卡台配置")
	}
	fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%q/%q/%q/%q", k.Login, k.Password, k.FundPassword, k.TOTPSecret)))
	if s.nexovcc.client == nil || fingerprint != s.nexovcc.fingerprint {
		s.nexovcc.client = nexovcc.New(k, s.nexovcc.transport)
		s.nexovcc.fingerprint = fingerprint
	}
	if s.nexovcc.transport == nil {
		s.nexovcc.client.SetRequestInterval(time.Duration(cfg.RequestIntervalSeconds) * time.Second)
	}
	return s.nexovcc.client, cfg, nil
}
func (s *Server) nexovccDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cards, e := s.store.NexovccCards()
	if e != nil {
		writeAPI(w, 500, nil, "读取环球卡片失败")
		return
	}
	rows := []map[string]any{}
	for _, c := range cards {
		local, _, _ := s.store.GPTPayCard(c.ID)
		rows = append(rows, map[string]any{"card": c, "local": local})
	}
	ops, e := s.store.NexovccOperations()
	if e != nil {
		writeAPI(w, 500, nil, "读取环球操作失败")
		return
	}
	cases, e := s.store.NexovccCases()
	if e != nil {
		writeAPI(w, 500, nil, "读取环球账号记录失败")
		return
	}
	var balance nexovcc.Balance
	products := []nexovcc.Product{}
	if e = s.store.NexovccCached("balance", &balance); e != nil {
		writeAPI(w, 500, nil, "读取余额缓存失败")
		return
	}
	if e = s.store.NexovccCached("products", &products); e != nil {
		writeAPI(w, 500, nil, "读取产品缓存失败")
		return
	}
	var finance nexovcc.Finance
	var financeError string
	if e = s.store.NexovccCached("finance", &finance); e != nil {
		writeAPI(w, 500, nil, "读取环球财务缓存失败")
		return
	}
	_ = s.store.NexovccCached("finance_error", &financeError)
	groups, e := s.store.NexovccTradeGroups(finance)
	if e != nil {
		writeAPI(w, 500, nil, "读取交易标记失败")
		return
	}
	cfg, _, e := s.store.NexovccSettings()
	if e != nil {
		writeAPI(w, 500, nil, "读取环球配置失败")
		return
	}
	syncState, e := s.nexovccSyncState(cfg)
	if e != nil {
		writeAPI(w, 500, nil, "读取环球刷新状态失败")
		return
	}
	reviewGroups, reviews, e := s.store.NexovccReviewData(finance)
	if e != nil {
		writeAPI(w, 500, nil, "读取交易核实记录失败")
		return
	}
	writeAPI(w, 200, map[string]any{"cards": rows, "operations": ops, "cases": cases, "balance": balance, "products": products, "finance": finance, "groups": groups, "review_groups": reviewGroups, "trade_reviews": reviews, "finance_error": financeError, "sync": syncState}, "")
}
func (s *Server) nexovccProgress(message string, done, total int) {
	s.nexovcc.jobMu.Lock()
	s.nexovcc.job.Message = message
	s.nexovcc.job.Done = done
	s.nexovcc.job.Total = total
	s.nexovcc.jobMu.Unlock()
}
func (s *Server) runNexovcc(w http.ResponseWriter, r *http.Request) {
	var in nexovccInput
	if decodeJSON(w, r, &in, 16<<10) != nil {
		return
	}
	allowed := map[string]bool{"sync": true, "finance": true, "balance": true, "open": true, "refresh": true, "deposit": true, "withdraw": true, "withdraw_all": true, "withdraw_cancel": true, "freeze": true, "unfreeze": true, "cancel": true, "remark": true, "resolve": true}
	if !allowed[in.Action] {
		writeAPI(w, 400, nil, "环球操作无效")
		return
	}
	if len(in.CardIDs) > 0 && (len(in.CardIDs) > 100 || !nexovcc.ValidRequestID(in.RequestID) || (in.Action != "withdraw_all" && !nexovccClosing(in.Action))) {
		writeAPI(w, 400, nil, "批量操作须选择 1～100 张卡，并提供有效请求编号")
		return
	}
	if in.Action != "sync" && in.Action != "finance" && in.Action != "balance" && in.Action != "refresh" && !in.Confirm {
		writeAPI(w, 400, nil, "请确认本次操作")
		return
	}
	if !s.nexovcc.mu.TryLock() {
		if in.Action == "sync" {
			s.nexovcc.jobMu.Lock()
			job := s.nexovcc.job
			s.nexovcc.jobMu.Unlock()
			if job.Status != "running" {
				job = nexovccJob{Status: "completed", Message: "环球正在处理其他操作，显示已保存的数据"}
			}
			writeAPI(w, 202, job, "")
			return
		}
		writeAPI(w, 409, nil, "环球正在执行操作，请等待当前操作完成")
		return
	}
	if in.Action == "sync" {
		cfg, _, err := s.store.NexovccSettings()
		var message string
		if err == nil {
			message, err = s.nexovccSyncWait(cfg, in.AutomaticRefresh)
		}
		if err != nil || message != "" {
			s.nexovcc.mu.Unlock()
			if err != nil {
				writeAPI(w, 500, nil, "读取环球刷新状态失败")
				return
			}
			writeAPI(w, 200, nexovccJob{Status: "completed", Message: message}, "")
			return
		}
	}
	if _, _, e := s.nexovccClientLocked(); e != nil {
		s.nexovcc.mu.Unlock()
		writeAPI(w, 400, nil, e.Error())
		return
	}
	s.nexovcc.jobMu.Lock()
	if s.nexovcc.closed {
		s.nexovcc.jobMu.Unlock()
		s.nexovcc.mu.Unlock()
		writeAPI(w, 503, nil, "服务正在关闭")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), max(15*time.Minute, time.Duration(len(in.CardIDs))*2*time.Minute))
	s.nexovcc.cancel = cancel
	s.nexovcc.job = nexovccJob{ID: randomRegistrationID(), Status: "running", Message: "正在处理…", Total: 1}
	job := s.nexovcc.job
	s.nexovcc.wg.Add(1)
	s.nexovcc.jobMu.Unlock()
	go func() {
		defer s.nexovcc.wg.Done()
		defer cancel()
		defer s.nexovcc.mu.Unlock()
		if len(in.CardIDs) > 0 {
			s.batchNexovccLocked(ctx, in)
			return
		}
		var op nexovcc.Operation
		var e error
		switch in.Action {
		case "sync":
			e = s.syncNexovccLocked(ctx)
		case "finance":
			e = s.syncNexovccFinanceLocked(ctx)
		case "balance":
			e = s.nexovccBalanceLocked(ctx)
		case "refresh":
			_, e = s.refreshNexovccLocked(ctx, in.CardID, "")
		case "open":
			op, e = s.openNexovccLocked(ctx, in.RequestID, in.TemplateID, false)
		case "resolve":
			op, e = s.resolveNexovccLocked(ctx, in)
		default:
			op, e = s.actionNexovccLocked(ctx, in)
		}
		s.nexovcc.jobMu.Lock()
		defer s.nexovcc.jobMu.Unlock()
		s.nexovcc.job.Status = "completed"
		s.nexovcc.job.Message = "操作完成"
		if op.ID != "" {
			s.nexovcc.job.Operation = &op
			if op.Status == "failed" {
				s.nexovcc.job.Status = "failed"
				s.nexovcc.job.Message = firstNonEmpty(op.Error, "环球操作未成功")
			} else if !op.Terminal() {
				s.nexovcc.job.Status = "attention"
				s.nexovcc.job.Message = op.Error
			}
		}
		if e != nil {
			s.nexovcc.job.Status = "failed"
			s.nexovcc.job.Message = e.Error()
		}
		if s.nexovcc.job.Status == "completed" {
			s.nexovcc.job.Done = s.nexovcc.job.Total
		}
	}()
	writeAPI(w, 202, job, "")
}
func (s *Server) nexovccBalanceLocked(ctx context.Context) error {
	c, _, e := s.nexovccClientLocked()
	if e != nil {
		return e
	}
	v, e := c.Balance(ctx)
	if e != nil {
		return e
	}
	return s.store.NexovccCache("balance", v)
}
func (s *Server) fetchNexovccLocked(ctx context.Context) error {
	// Reconcile completed deletions before unrelated supplier queries. A
	// temporary balance/product/ledger outage must not hide proof already saved.
	priorOps, err := s.store.NexovccOperations()
	if err != nil {
		return err
	}
	for _, op := range priorOps {
		if updated, confirmed := s.confirmCachedNexovccCloseLocked(op); confirmed {
			if err = s.store.SaveNexovccOperation(updated); err != nil {
				return err
			}
		}
	}
	c, _, e := s.nexovccClientLocked()
	if e != nil {
		return e
	}
	s.nexovccProgress("查询账户余额", 0, 3)
	if e = s.nexovccBalanceLocked(ctx); e != nil {
		return e
	}
	s.nexovccProgress("同步产品 / BIN", 1, 3)
	if e = s.syncNexovccProductsIfDueLocked(ctx); e != nil {
		return e
	}
	s.nexovccProgress("分页同步卡片", 2, 3)
	rows, e := c.Pages(ctx, "/api/card/findUserCardList")
	if e != nil {
		return e
	}
	failures := 0
	for i, r := range rows {
		if e = ctx.Err(); e != nil {
			return e
		}
		s.nexovccProgress(fmt.Sprintf("同步卡片 %d / %d", i+1, len(rows)), i+2, len(rows)+2)
		card, e := nexovcc.CardFrom(r)
		if e != nil {
			return e
		}
		card, e = s.store.SaveNexovccCard(card, nil)
		if e != nil {
			return e
		}
		if card.Status == "4" {
			continue
		}
		if !card.Imported || card.Error != "" {
			if _, e = s.refreshNexovccLocked(ctx, card.ID, ""); e != nil {
				failures++
			}
		} else {
			// The list often omits balance. Imported cards still need the live
			// balance endpoint after payments/refunds; an import is not a refresh.
			updated, balanceErr := s.readNexovccCardBalanceLocked(ctx, card)
			if balanceErr != nil {
				updated.Error = "余额同步失败：" + balanceErr.Error()
				failures++
			}
			if _, e = s.store.SaveNexovccCard(updated, nil); e != nil {
				return e
			}
		}
	}
	if e = s.syncNexovccFinanceLocked(ctx); e != nil {
		return fmt.Errorf("卡片已同步，财务同步未完成：%w", e)
	}
	// Reconcile submitted steps without replaying them. Explicitly requested
	// close pipelines may advance to their next unsent step after confirmation.
	ops, e := s.store.NexovccOperations()
	if e != nil {
		return e
	}
	for _, op := range ops {
		if !op.Terminal() && op.Serial != "" {
			op = s.verifyNexovccOperationLocked(ctx, op)
			if e = s.store.SaveNexovccOperation(op); e != nil {
				return e
			}
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d 张卡余额或资料同步失败，请稍后重新同步", failures)
	}
	return nil
}
func (s *Server) refreshNexovccLocked(ctx context.Context, id, serial string) (nexovcc.Card, error) {
	if id == "" && serial != "" {
		id = nexovcc.CardID(serial)
	}
	// Deleted cards have no detail or balance endpoint on the supplier. Their
	// terminal status from the card list remains usable for receipt matching.
	if old, err := s.store.NexovccCard(id); err == nil && old.Status == "4" {
		if serial != "" && old.Serial != serial {
			return nexovcc.Card{}, fmt.Errorf("环球返回了不同卡片，请核实")
		}
		return old, nil
	}
	c, _, e := s.nexovccClientLocked()
	if e != nil {
		return nexovcc.Card{}, e
	}
	if serial == "" {
		old, e := s.store.NexovccCard(id)
		if e != nil {
			return old, e
		}
		serial = old.Serial
	}
	data, e := c.Do(ctx, "/api/card/getUserCardDetail", map[string]any{"serial_no": serial})
	if e != nil {
		return nexovcc.Card{}, e
	}
	if data == nil {
		// A successful deletion returns code=0/data=null from the detail
		// endpoint. Null alone is not evidence: require the exact card in the
		// paginated list with the documented deleted status.
		rows, err := c.Pages(ctx, "/api/card/findUserCardList")
		if err != nil {
			return nexovcc.Card{}, err
		}
		for _, row := range rows {
			if nexovcc.String(row["serial_no"]) != serial || nexovcc.String(row["status"]) != "4" {
				continue
			}
			card, err := nexovcc.CardFrom(row)
			if err != nil {
				return card, err
			}
			return s.store.SaveNexovccCard(card, nil)
		}
		return nexovcc.Card{}, fmt.Errorf("卡片详情暂未返回，等待卡列表同步状态")
	}
	m, e := nexovcc.Object(data)
	if e != nil {
		return nexovcc.Card{}, e
	}
	if nexovcc.String(m["serial_no"]) == "" {
		m["serial_no"] = serial
	}
	if nexovcc.String(m["serial_no"]) != serial {
		return nexovcc.Card{}, fmt.Errorf("环球返回了不同卡片，请核实")
	}
	card, e := nexovcc.CardFrom(m)
	if e != nil {
		return card, e
	}
	private, pe := nexovcc.PrivateFrom(m)
	if card.Status == "4" {
		return s.store.SaveNexovccCard(card, nil)
	}
	if pe != nil {
		card.Error = "卡密未完整导入，请重新同步"
		_, _ = s.store.SaveNexovccCard(card, nil)
		return card, pe
	}
	card, e = s.readNexovccCardBalanceLocked(ctx, card)
	if e != nil {
		return card, e
	}
	saved, e := s.store.SaveNexovccCard(card, &private)
	if e != nil {
		card.Error = "卡密校验未通过，未加入可用卡"
		_, _ = s.store.SaveNexovccCard(card, nil)
	}
	return saved, e
}

func (s *Server) readNexovccCardBalanceLocked(ctx context.Context, card nexovcc.Card) (nexovcc.Card, error) {
	if card.Status == "4" {
		return card, nil
	}
	c, _, e := s.nexovccClientLocked()
	if e != nil {
		return card, e
	}
	data, e := c.Do(ctx, "/api/card/getUserCardBalance", map[string]any{"serial_no": card.Serial})
	if e != nil {
		return card, e
	}
	balance, e := nexovcc.Object(data)
	if e != nil {
		return card, e
	}
	n, e := nexovcc.Cents(nexovcc.String(balance["balance"]))
	if e != nil {
		return card, fmt.Errorf("环球卡余额缺失或无效")
	}
	card.Balance = nexovcc.Money(n)
	card.BalanceAt = time.Now()
	return card, nil
}
func (s *Server) openNexovccLocked(ctx context.Context, id, templateID string, automatic bool, checks ...func() error) (nexovcc.Operation, error) {
	var op nexovcc.Operation
	if !nexovcc.ValidRequestID(id) {
		return op, fmt.Errorf("开卡请求编号无效")
	}
	prior, e := s.store.NexovccOperation(id)
	if e == nil {
		if prior.Action != "open" || prior.TemplateID != templateID || prior.Automatic != automatic {
			return prior, fmt.Errorf("请求编号已用于其他参数")
		}
		return prior, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return op, e
	}
	client, cfg, e := s.nexovccClientLocked()
	if e != nil {
		return op, e
	}
	if automatic && !cfg.Enabled {
		return op, fmt.Errorf("环球卡台已停用，不能自动开卡")
	}
	var t *nexovcc.Template
	for i := range cfg.Templates {
		if cfg.Templates[i].ID == templateID {
			t = &cfg.Templates[i]
		}
	}
	if t == nil {
		return op, fmt.Errorf("请选择已保存的环球开卡模板")
	}
	if automatic {
		pro, _, _, err := s.store.ProSettings()
		if err != nil {
			return op, err
		}
		pro, _, err = s.restockCardSettings(ctx, pro)
		if err != nil {
			return op, err
		}
		if pro.PurchaseRoute != nil && (t.ID != pro.PurchaseRoute.TemplateID || t.PlanCode != pro.PurchaseRoute.PlanCode) {
			return op, fmt.Errorf("环球模板适用套餐已变化，未开卡")
		}
	}
	// Query current availability; a cached product may have been delisted.
	products, e := s.syncNexovccProductsLocked(ctx)
	if e != nil {
		return op, e
	}
	product, e := t.SelectProduct(products)
	if e != nil {
		return op, e
	}
	amount, _ := nexovcc.Cents(t.Amount)
	taskID, _ := ctx.Value(proRestockContextKey{}).(string)
	manualProvider, _ := ctx.Value(proManualCardProviderKey{}).(string)
	if route, _ := ctx.Value(proPurchaseContextKey{}).(*model.ProPurchaseRoute); route != nil {
		manualProvider = route.CardProvider
	}
	admission := store.NexovccOpenAdmission{RestockID: taskID, ManualProvider: manualProvider}
	if cfg.MaxOnlineCards > 0 || cfg.AutoRecycleAt > 0 {
		if e = s.store.CheckNexovccOpen(nexovcc.Operation{Action: "open", Amount: t.Amount, Automatic: automatic, CreatedAt: time.Now()}, admission); e != nil {
			return op, e
		}
		if e = s.ensureNexovccOnlineSlotLocked(ctx, id, cfg, checks...); e != nil {
			return op, e
		}
		products, e = s.syncNexovccProductsLocked(ctx)
		if e != nil {
			return op, e
		}
		product, e = t.SelectProduct(products)
		if e != nil {
			return op, e
		}
	}
	for _, check := range checks {
		if e = check(); e != nil {
			return op, e
		}
	}
	if e = ctx.Err(); e != nil {
		return op, e
	}
	balance, e := client.Balance(ctx)
	if e != nil {
		return op, e
	}
	if e = s.store.NexovccCache("balance", balance); e != nil {
		return op, e
	}
	available, _ := nexovcc.Cents(balance.Amount)
	buffer, _ := nexovcc.Cents(t.FeeBuffer)
	if available < amount+buffer {
		return op, fmt.Errorf("环球账户余额不足以覆盖开卡金额和预留费用")
	}
	op = nexovcc.Operation{PlanCode: t.PlanCode, ID: id, Action: "open", TemplateID: templateID, ProductID: product.ID, Amount: t.Amount, Remark: id, Status: "submitted", Automatic: automatic, BeforeAccountBalance: balance.Amount, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if e = s.store.BeginNexovccOperation(op, admission); e != nil {
		return op, e
	}
	pid, _ := strconv.ParseInt(product.ID, 10, 64)
	body := map[string]any{"pid": pid, "money_id": 3, "holder_id_rand": true, "amount": t.Amount, "remark": id, "open_num": 1, "allow_consume_scene": "1,3,4,5,6,7,8,9", "upload_serial": ""}
	if t.HolderID != "" {
		body["holder_id_rand"] = false
		body["holder_id"], _ = strconv.ParseInt(t.HolderID, 10, 64)
	}
	data, e := client.Do(ctx, "/api/card/createUserCard", body)
	if e != nil {
		op.Status = "unknown"
		if !nexovcc.IsUnknown(e) {
			op.Status = "failed"
		}
		op.Error = e.Error()
	} else {
		m, err := nexovcc.Object(data)
		if err == nil {
			op.Serial = nexovcc.String(m["serial_no"])
		}
		if op.Serial == "" {
			op.Status = "unknown"
			op.Error = "开卡响应未返回 serial_no，请同步卡片并核实原操作，勿重复开卡"
		} else {
			op.CardID = nexovcc.CardID(op.Serial)
			op.Status = "import_pending"
			op.Error = "开卡已返回序列号，等待卡片资料同步"
			if e = s.store.SaveNexovccOperation(op); e != nil {
				return op, e
			}
			if _, e = s.refreshNexovccLocked(ctx, op.CardID, op.Serial); e != nil {
				op.Error = "开卡已完成，资料同步未完成：" + e.Error()
			} else {
				op.Status = "succeeded"
				op.Error = ""
			}
		}
	}
	if e = s.store.SaveNexovccOperation(op); e != nil {
		return op, e
	}
	s.notifyNexovccOpened(op)
	return op, nil
}
func (s *Server) verifyNexovccOperationLocked(ctx context.Context, op nexovcc.Operation) nexovcc.Operation {
	if updated, confirmed := s.confirmCachedNexovccCloseLocked(op); confirmed {
		return updated
	}
	if op.Action == "freeze" && op.Status == "processing" {
		op.Status, op.Error, op.Evidence, op.StepAt = "succeeded", "", "卡台已确认冻结成功", op.UpdatedAt
		return op
	}
	if nexovccClosing(op.Action) && op.Step != "" && !strings.HasPrefix(op.Step, "cancel_") {
		if op.Step == "freeze_submitting" {
			op.Error = "冻结结果尚未确认，请查询原操作；尚未提交销卡"
			return op
		}
		updated, err := s.advanceNexovccCloseLocked(ctx, op)
		if err != nil {
			updated.Error = "操作已保留，稍后继续：" + err.Error()
		}
		return updated
	}
	card, e := s.refreshNexovccLocked(ctx, op.CardID, op.Serial)
	if e != nil {
		op.Error = "结果暂未同步，系统会自动继续查询：" + e.Error()
		return op
	}
	confirmed := false
	switch op.Action {
	case "open":
		confirmed = card.Imported
	case "remark":
		confirmed = card.Remark == op.Remark
	case "cancel", "withdraw_cancel":
		if card.Status == "4" {
			confirmed = s.nexovccTransferConfirmedLocked(ctx, &op, 3002, op.BeforeBalance)
			if !confirmed {
				op.Error = "卡台已销卡，正在自动同步余款到账"
			}
		} else {
			op.Error = "正在自动同步销卡状态及余款到账"
		}
	case "freeze", "unfreeze":
		if op.Action == "unfreeze" {
			confirmed = card.Status == "1"
		} else {
			confirmed = card.Control == "frozen"
			if !confirmed {
				op.Error = "冻结结果尚未确认，请查询原操作"
			}
		}
	case "deposit", "withdraw", "withdraw_all":
		kind := 4002
		if op.Action == "deposit" {
			kind = 4001
		}
		confirmed = s.nexovccTransferConfirmedLocked(ctx, &op, kind, op.Amount)
		if !confirmed {
			op.Error = "请求已受理，等待同卡资金流水确认到账；可稍后同步或查询原操作"
		}
	}
	if confirmed {
		op.Status = "succeeded"
		op.Error = ""
	}
	return op
}
func (s *Server) resolveNexovccLocked(ctx context.Context, in nexovccInput) (nexovcc.Operation, error) {
	op, e := s.store.NexovccOperation(in.RequestID)
	if e != nil {
		return op, e
	}
	if op.Terminal() {
		return op, nil
	}
	if in.Outcome == "query" {
		if updated, confirmed := s.confirmCachedNexovccCloseLocked(op); confirmed {
			return updated, s.store.SaveNexovccOperation(updated)
		}
		if op.Action == "withdraw" || op.Action == "withdraw_all" || op.Action == "deposit" || nexovccClosing(op.Action) {
			if e = s.syncNexovccFinanceLocked(ctx); e != nil {
				return op, e
			}
		}
		if op.Action == "open" && op.Serial == "" {
			if in.Serial == "" {
				return op, fmt.Errorf("请先同步卡片，在环球后台核对操作编号备注后填写对应序列号")
			}
			card, e := s.refreshNexovccLocked(ctx, "", in.Serial)
			if e != nil {
				return op, e
			}
			if card.Remark != op.Remark || card.ProductID != op.ProductID {
				return op, fmt.Errorf("卡片备注或产品与原开卡操作不一致，不能自动关联")
			}
			op.Serial = card.Serial
			op.CardID = card.ID
		}
		op = s.verifyNexovccOperationLocked(ctx, op)
	} else {
		if len([]rune(strings.TrimSpace(in.Evidence))) < 5 {
			return op, fmt.Errorf("请填写至少 5 字的实际核实依据")
		}
		if in.Outcome != "failed" && in.Outcome != "succeeded" {
			return op, fmt.Errorf("核实结果无效")
		}
		if op.Action == "open" && in.Outcome == "succeeded" {
			return op, fmt.Errorf("开卡请通过查询原卡并同步入库确认成功")
		}
		if op.Action == "open" && op.Serial != "" && in.Outcome == "failed" {
			return op, fmt.Errorf("上游已返回卡片，不能改为失败；请同步原卡")
		}
		if in.Outcome == "succeeded" {
			card, e := s.refreshNexovccLocked(ctx, op.CardID, op.Serial)
			if e != nil {
				return op, e
			}
			if nexovccClosing(op.Action) && card.Status != "4" {
				return op, fmt.Errorf("卡台尚未返回已销卡状态")
			}
			if op.Action == "freeze" && card.Status == "1" {
				return op, fmt.Errorf("卡台仍返回正常状态，不能确认冻结")
			}
			if op.Action == "unfreeze" && card.Status != "1" {
				return op, fmt.Errorf("卡台尚未返回正常状态")
			}
		}
		op.Status = in.Outcome
		op.Evidence = in.Evidence
		op.Error = ""
	}
	e = s.store.SaveNexovccOperation(op)
	return op, e
}
func (s *Server) nexovccPrivate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	card, e := s.store.NexovccCard(r.PathValue("id"))
	if e != nil {
		writeAPI(w, 404, nil, "卡片不存在")
		return
	}
	_, secret, e := s.store.GPTPayCard(card.ID)
	if e != nil {
		writeAPI(w, 400, nil, "请先同步完整卡信息")
		return
	}
	writeAPI(w, 200, secret, "")
}
func (s *Server) nexovccCaseState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		State    string `json:"state"`
		Reason   string `json:"reason"`
		Revision int    `json:"revision"`
		Confirm  bool   `json:"confirm"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	if !in.Confirm {
		writeAPI(w, 400, nil, "请确认回款状态")
		return
	}
	if e := s.store.SetNexovccCase(r.PathValue("id"), in.State, in.Reason, in.Revision); e != nil {
		writeAPI(w, 400, nil, e.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"saved": true}, "")
}
func (s *Server) validateNexovccAutomation(v model.ProSettings) error {
	if !v.NexovccAutoOpen {
		return nil
	}
	if v.NexovccDailyOpenLimit < 1 || v.NexovccDailyOpenLimit > 1000 {
		return fmt.Errorf("环球每日开卡数量须为 1～1000")
	}
	if n, e := nexovcc.Cents(v.NexovccDailyAmountLimit); e != nil || n <= 0 {
		return fmt.Errorf("请设置环球每日开卡金额预算")
	}
	cfg, _, e := s.store.NexovccSettings()
	if e != nil {
		return e
	}
	if !cfg.Enabled {
		return fmt.Errorf("请先启用环球卡台")
	}
	for _, t := range cfg.Templates {
		if t.ID == v.NexovccTemplateID {
			return nil
		}
	}
	return fmt.Errorf("请选择已保存的环球开卡模板")
}
func (s *Server) ensureProviderCard(ctx context.Context, checks ...func() error) (gptpay.Card, error) {
	p, _, _, e := s.store.ProSettings()
	if e != nil {
		return gptpay.Card{}, e
	}
	p, _, e = s.restockCardSettings(ctx, p)
	if e != nil {
		return gptpay.Card{}, e
	}
	if p.AutoCardProvider() == "nexovcc" {
		return s.ensureNexovccCard(ctx, checks...)
	}
	return s.ensureEJHCard(ctx, checks...)
}
func (s *Server) ensureNexovccCard(ctx context.Context, checks ...func() error) (gptpay.Card, error) {
	s.nexovcc.mu.Lock()
	defer s.nexovcc.mu.Unlock()
	p, _, _, e := s.store.ProSettings()
	if e != nil {
		return gptpay.Card{}, e
	}
	p, strict, e := s.restockCardSettings(ctx, p)
	if e != nil {
		return gptpay.Card{}, e
	}
	cards, e := s.automaticProCards(p, strict)
	if e != nil {
		return gptpay.Card{}, e
	}
	for _, card := range cards {
		if card.Source == "nexovcc" {
			return card, nil
		}
	}
	p, _, _, e = s.store.ProSettings()
	if e == nil {
		p, _, e = s.restockCardSettings(ctx, p)
	}
	if e != nil {
		return gptpay.Card{}, e
	}
	if !p.NexovccAutoOpen {
		return gptpay.Card{}, fmt.Errorf("环球自动补卡未启用")
	}
	if e = s.validateNexovccAutomation(p); e != nil {
		return gptpay.Card{}, e
	}
	all, _, e := s.store.GPTPayCards(int(^uint(0)>>1), 0, false)
	if e != nil {
		return gptpay.Card{}, e
	}
	for _, card := range all {
		if card.Source != "nexovcc" {
			continue
		}
		for _, o := range card.Occupants {
			if o.Deleted || o.Status == "submission_unknown" || o.Status == "manual_review" {
				return gptpay.Card{}, fmt.Errorf("环球卡 %s 存在未核实付款占用，请先处理原订单", card.Last4)
			}
		}
	}
	ops, e := s.store.NexovccOperations()
	if e != nil {
		return gptpay.Card{}, e
	}
	for _, op := range ops {
		if op.Automatic && op.Status == "succeeded" && op.Action == "open" {
			prior, _, err := s.store.GPTPayCard(op.CardID)
			if err != nil {
				return gptpay.Card{}, fmt.Errorf("已开环球卡尚未加入银行卡列表，请同步原卡后继续")
			}
			if prior.IssuerStatus != "4" && prior.OpenedAccounts == 0 && prior.PendingAccounts == 0 && prior.ActiveAccounts == 0 && !prior.Available {
				return gptpay.Card{}, fmt.Errorf("环球卡尾号 %s 尚未使用且不可用，请核实原卡，暂停自动补卡", prior.Last4)
			}
		}
		if op.Action == "open" && !op.Terminal() {
			return gptpay.Card{}, fmt.Errorf("存在未完成环球开卡，请在操作记录核实或同步原卡")
		}
		if op.Automatic && op.Status == "failed" && time.Since(op.UpdatedAt) < 5*time.Minute {
			return gptpay.Card{}, fmt.Errorf("环球开卡失败后冷却 5 分钟")
		}
	}
	if e = ctx.Err(); e != nil {
		return gptpay.Card{}, e
	}
	for _, check := range checks {
		if e = check(); e != nil {
			return gptpay.Card{}, e
		}
	}
	sum := sha256.Sum256([]byte(time.Now().String()))
	id := fmt.Sprintf("nex-%x", sum[:12])
	op, e := s.openNexovccLocked(ctx, id, p.NexovccTemplateID, true, checks...)
	if e != nil {
		return gptpay.Card{}, e
	}
	if op.Status == "import_pending" && op.Serial != "" {
		op, e = s.waitNexovccImportLocked(ctx, op, 45*time.Second, 3*time.Second, checks...)
		if e != nil {
			return gptpay.Card{}, e
		}
	}
	if op.Status != "succeeded" {
		return gptpay.Card{}, fmt.Errorf("环球自动开卡待核实：%s", op.Error)
	}
	card, _, e := s.store.GPTPayCard(op.CardID)
	if e == nil && !card.Available {
		e = fmt.Errorf("环球卡已开出但暂不可用，请核实原卡")
	}
	return card, e
}

// Issuance can return a serial before the card number/expiry are ready. Keep
// this startup attached to that exact operation; only repeat reads, never issue
// another card. The caller holds the issuer and Pro allocation locks.
func (s *Server) waitNexovccImportLocked(parent context.Context, op nexovcc.Operation, timeout, interval time.Duration, checks ...func() error) (nexovcc.Operation, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	for op.Action == "open" && op.Status == "import_pending" && op.Serial != "" {
		for _, check := range checks {
			if err := check(); err != nil {
				return op, err
			}
		}
		s.proAutoMu.Lock()
		closed := s.proAutoClosed
		s.proAutoMu.Unlock()
		if closed {
			return op, fmt.Errorf("服务正在停止，已保留原开卡记录")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return op, fmt.Errorf("环球卡已开出，等待卡片资料未完成；原卡已保留，请同步后重试当前账号：%s", op.Error)
		case <-timer.C:
		}
		for _, check := range checks {
			if err := check(); err != nil {
				return op, err
			}
		}
		op = s.verifyNexovccOperationLocked(ctx, op)
		if err := s.store.SaveNexovccOperation(op); err != nil {
			return op, err
		}
	}
	return op, nil
}
func (s *Server) notifyNexovccOpened(op nexovcc.Operation) {
	result := map[string]string{"succeeded": "成功", "failed": "失败", "unknown": "结果待核实", "import_pending": "已开出，待同步"}[op.Status]
	s.queueNotification("nexovcc_open", "环球开卡"+result, fmt.Sprintf("开卡金额：%s USD\n操作编号：%s\n请在环球卡台查看详情", op.Amount, op.ID))
}
