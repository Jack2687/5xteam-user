package nexovcc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	_ "embed"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed transport.py
var transportScript string

type Request struct {
	Path   string         `json:"path"`
	Body   map[string]any `json:"body"`
	Cookie string         `json:"cookie"`
}
type Response struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Cookie    string `json:"cookie"`
	Error     string `json:"error"`
	Uncertain bool   `json:"uncertain"`
}
type Transport func(context.Context, Request) (Response, error)
type Error struct {
	Message string
	Unknown bool
	Code    int
	RetryAt time.Time
}

func (e *Error) Error() string { return e.Message }
func IsUnknown(err error) bool { var e *Error; return !errors.As(err, &e) || e.Unknown }

type Client struct {
	mu              sync.Mutex
	credentials     Credentials
	cookie, userID  string
	until           time.Time
	transport       Transport
	now             func() time.Time
	requestInterval time.Duration
	nextRequest     time.Time
	retryAt         time.Time
	backoffFailures int
	wait            func(context.Context, time.Duration) error
}

func New(c Credentials, transport Transport) *Client {
	interval := time.Duration(0) // Injected transports own their test clock.
	if transport == nil {
		transport = PythonTransport
		interval = 3 * time.Second
	}
	return &Client{credentials: c, transport: transport, now: time.Now, requestInterval: interval, wait: waitRequest}
}

func waitRequest(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) SetRequestInterval(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestInterval = delay
}

func (c *Client) RetryAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retryAt
}

func (c *Client) coolingDown() error {
	if c.now().Before(c.retryAt) {
		seconds := int(c.retryAt.Sub(c.now()).Seconds()) + 1
		return &Error{Message: fmt.Sprintf("环球接口冷却中，约 %d 秒后重试", seconds), Code: 429, RetryAt: c.retryAt}
	}
	return nil
}

func (c *Client) backoff() time.Time {
	c.backoffFailures++
	delay := 30 * time.Second * time.Duration(1<<min(c.backoffFailures-1, 4))
	c.retryAt = c.now().Add(min(delay, 5*time.Minute))
	return c.retryAt
}
func PythonTransport(ctx context.Context, r Request) (Response, error) {
	var v Response
	python := ""
	for _, name := range []string{"python", "python3", "py"} {
		if p, e := exec.LookPath(name); e == nil {
			python = p
			break
		}
	}
	if python == "" {
		return v, fmt.Errorf("环球卡台需要 Python 和 curl_cffi")
	}
	raw, _ := json.Marshal(r)
	cmd := exec.CommandContext(ctx, python, "-c", transportScript)
	cmd.Stdin = bytes.NewReader(raw)
	out, e := cmd.Output()
	if e != nil {
		return v, fmt.Errorf("环球请求进程中断，请核实操作结果")
	}
	if len(out) > 16<<20 || json.Unmarshal(out, &v) != nil {
		return v, fmt.Errorf("环球请求响应无法解析")
	}
	return v, nil
}
func TOTP(secret string, now time.Time) (string, error) {
	secret = strings.ToUpper(strings.Join(strings.Fields(secret), ""))
	secret = strings.TrimRight(secret, "=")
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if e != nil || len(key) == 0 {
		return "", fmt.Errorf("谷歌验证密钥不是有效 Base32")
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(now.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(b[:])
	h := m.Sum(nil)
	i := h[len(h)-1] & 15
	n := binary.BigEndian.Uint32(h[i:i+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000), nil
}
func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func flag(v any) bool { s := strings.ToLower(text(v)); return s == "true" || s == "1" }

var cardDigits = regexp.MustCompile(`\b\d{12,19}\b`)
var privateField = regexp.MustCompile(`(?i)(cvv(?:password)?|cvc|card_no|cardNumber|expire_at_str|money_password|password|verify_code|VCC-Web)["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;{}]+)`)

func (c *Client) safe(s string) string {
	for _, v := range []string{c.credentials.Login, c.credentials.Password, c.credentials.FundPassword, c.credentials.TOTPSecret, c.cookie} {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[已隐藏]")
		}
	}
	s = cardDigits.ReplaceAllString(s, "[卡号已隐藏]")
	s = privateField.ReplaceAllString(s, "[敏感字段已隐藏]")
	if len([]rune(s)) > 300 {
		s = string([]rune(s)[:300])
	}
	return s
}
func (c *Client) call(ctx context.Context, path string, body map[string]any) (any, error) {
	if err := c.coolingDown(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Message: err.Error()}
	}
	if delay := c.nextRequest.Sub(c.now()); delay > 0 {
		if err := c.wait(ctx, delay); err != nil {
			return nil, &Error{Message: err.Error()}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Message: err.Error()}
	}
	if path == "/api/verifyGoogleCode" {
		// Generate after pacing; a code prepared before the wait can expire.
		otp, err := TOTP(c.credentials.TOTPSecret, c.now())
		if err != nil {
			return nil, &Error{Message: err.Error()}
		}
		body["verify_code"] = otp
	}
	r, e := c.transport(ctx, Request{path, body, c.cookie})
	c.nextRequest = c.now().Add(c.requestInterval)
	if e != nil {
		return nil, &Error{Message: c.safe(e.Error()), Unknown: true}
	}
	if r.Error != "" {
		return nil, &Error{Message: c.safe(r.Error), Unknown: r.Uncertain}
	}
	if r.Status < 200 || r.Status >= 300 {
		err := &Error{Message: fmt.Sprintf("环球接口 HTTP %d", r.Status), Unknown: true}
		if r.Status == 429 || r.Status == 522 {
			err.RetryAt = c.backoff()
		}
		slog.Warn("环球接口请求失败", "path", path, "http_status", r.Status)
		return nil, err
	}
	var v map[string]any
	dec := json.NewDecoder(strings.NewReader(r.Body))
	dec.UseNumber()
	if dec.Decode(&v) != nil || v == nil {
		return nil, &Error{Message: "环球响应不是有效 JSON", Unknown: true}
	}
	code, e := strconv.Atoi(text(v["code"]))
	if e != nil {
		return nil, &Error{Message: "环球响应缺少有效业务状态码", Unknown: true}
	}
	if code != 0 {
		msg := text(v["message"])
		if msg == "" {
			msg = text(v["msg"])
		}
		err := &Error{Message: fmt.Sprintf("环球 %d：%s", code, c.safe(msg)), Code: code}
		lower := strings.ToLower(msg)
		if code == 429 || strings.Contains(msg, "频繁") || strings.Contains(lower, "too many") || strings.Contains(lower, "rate limit") {
			err.RetryAt = c.backoff()
		}
		slog.Warn("环球接口业务失败", "path", path, "code", code, "retry_at", err.RetryAt)
		return nil, err
	}
	if r.Cookie != "" {
		c.cookie = r.Cookie
	}
	// A login alone does not prove the rate-limited business endpoint recovered.
	if path != "/api/login" && path != "/api/verifyGoogleCode" {
		c.backoffFailures = 0
		c.retryAt = time.Time{}
	}
	return v["data"], nil
}
func (c *Client) login(ctx context.Context) error {
	c.cookie = ""
	c.userID = ""
	c.until = time.Time{}
	data, e := c.call(ctx, "/api/login", map[string]any{"tel": c.credentials.Login, "password": c.credentials.Password})
	if e != nil {
		return fmt.Errorf("环球登录失败：%w", e)
	}
	m, ok := data.(map[string]any)
	if !ok || text(m["user_id"]) == "" || c.cookie == "" {
		return &Error{Message: "环球登录未返回 user_id 或登录 Cookie"}
	}
	c.userID = text(m["user_id"])
	if flag(m["is_bind_ga"]) || flag(m["need_verify"]) {
		if _, e = c.call(ctx, "/api/verifyGoogleCode", map[string]any{"is_login": true, "user_id": c.userID}); e != nil {
			return fmt.Errorf("环球谷歌验证失败：%w", e)
		}
	}
	c.until = c.now().Add(20*time.Minute - 90*time.Second)
	return nil
}

var paths = map[string]bool{"/api/getBalanceInfo": true, "/api/product/findUserProductList": true, "/api/card/findUserCardList": true, "/api/card/getUserCardDetail": true, "/api/card/getUserCardBalance": true, "/api/card/createUserCard": true, "/api/card/userCardDeposit": true, "/api/card/userCardWithdraw": true, "/api/card/lockUserCard": true, "/api/card/unLockUserCard": true, "/api/card/deleteUserCard": true, "/api/card/updateCardRemark": true}

func (c *Client) Do(ctx context.Context, path string, body map[string]any) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.coolingDown(); err != nil {
		return nil, err
	}
	if !paths[path] && path != "/api/findLogRecharge" && path != "/api/findLogCardTrades" && path != "/api/findLogWebFunds" && path != "/api/findLogReportCardDay" {
		return nil, &Error{Message: "环球接口路径无效"}
	}
	if !c.now().Before(c.until) {
		if e := c.login(ctx); e != nil {
			return nil, e
		}
	}
	p := map[string]any{}
	for k, v := range body {
		p[k] = v
	}
	for attempt := 0; attempt < 2; attempt++ {
		p["user_id"] = c.userID
		if path == "/api/card/getUserCardDetail" || path == "/api/card/userCardDeposit" || path == "/api/card/userCardWithdraw" || path == "/api/card/lockUserCard" || path == "/api/card/unLockUserCard" || path == "/api/card/deleteUserCard" || path == "/api/card/updateCardRemark" {
			p["money_password"] = c.credentials.FundPassword
			p["password"] = c.credentials.FundPassword
		}
		v, e := c.call(ctx, path, p)
		if e == nil {
			return v, nil
		}
		if a, ok := e.(*Error); ok && (a.Code == 50001 || a.Code == 50008 || a.Code == 50009) {
			c.until = time.Time{}
			if attempt == 0 {
				if e = c.login(ctx); e != nil {
					return nil, e
				}
				continue
			}
		}
		return nil, e
	}
	return nil, &Error{Message: "环球登录状态失效"}
}
func (c *Client) Balance(ctx context.Context) (Balance, error) {
	v, e := c.Do(ctx, "/api/getBalanceInfo", nil)
	if e != nil {
		return Balance{}, e
	}
	rows, ok := v.([]any)
	if !ok {
		return Balance{}, fmt.Errorf("环球余额响应缺少账户列表")
	}
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok || text(m["money_id"]) != "3" {
			continue
		}
		a, e := Cents(text(m["balance"]))
		if e != nil {
			return Balance{}, e
		}
		f, e := Cents(text(m["freeze_balance"]))
		if e != nil {
			return Balance{}, fmt.Errorf("环球冻结余额缺失或格式无效")
		}
		return Balance{Money(a), Money(f), time.Now()}, nil
	}
	return Balance{}, fmt.Errorf("环球没有返回 USD 账户")
}

// Lists in observed SPA versions use data.list/records/rows or a direct array.
// Reject unknown envelopes instead of treating them as an empty successful sync.
func List(v any) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		found := false
		for _, k := range []string{"list", "records", "rows"} {
			if a, yes := m[k]; yes {
				// Empty card accounts return list:null with total:0. Require
				// the explicit zero so incomplete responses still fail sync.
				if a == nil && text(m["total"]) == "0" {
					return []map[string]any{}, nil
				}
				v = a
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("环球列表响应结构未识别")
		}
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("环球列表响应格式无效")
	}
	out := []map[string]any{}
	for _, r := range a {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("环球列表条目无效")
		}
		out = append(out, m)
	}
	return out, nil
}
func (c *Client) Pages(ctx context.Context, path string) ([]map[string]any, error) {
	all := []map[string]any{}
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		v, e := c.Do(ctx, path, map[string]any{"page": page, "pageSize": 100})
		if e != nil {
			return nil, e
		}
		rows, e := List(v)
		if e != nil {
			return nil, e
		}
		raw, _ := json.Marshal(rows)
		key := string(raw)
		if len(rows) > 0 && seen[key] {
			return nil, fmt.Errorf("环球分页重复，停止同步")
		}
		seen[key] = true
		all = append(all, rows...)
		if len(rows) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("环球列表超过 100 页，请核实后分批同步")
}
func ProductFrom(m map[string]any) (Product, error) {
	id := text(m["ID"])
	if id == "" {
		id = text(m["id"])
	}
	n, e := strconv.ParseInt(id, 10, 64)
	if e != nil || n < 1 {
		return Product{}, fmt.Errorf("环球产品缺少有效 ID")
	}
	minimum := text(m["recharge_min"])
	if _, e = Cents(minimum); e != nil {
		return Product{}, e
	}
	return Product{id, text(m["product_name"]), text(m["card_range"]), text(m["card_type"]), minimum}, nil
}
func ActiveProduct(m map[string]any) bool { return text(m["status"]) == "1" }
func CardFrom(m map[string]any) (Card, error) {
	serial := text(m["serial_no"])
	if serial == "" {
		return Card{}, fmt.Errorf("环球卡片缺少 serial_no")
	}
	number := text(m["card_no"])
	last := ""
	if len(number) >= 4 {
		last = number[len(number)-4:]
	}
	c := Card{Serial: serial, Status: text(m["status"]), Last4: last, BIN: text(m["card_range"]), ProductID: text(m["pid"]), Remark: text(m["remark"]), SyncedAt: time.Now()}
	if c.ProductID == "" {
		c.ProductID = text(m["product_id"])
	}
	if c.Status == "" {
		return c, fmt.Errorf("环球卡片缺少状态")
	}
	if m["balance"] != nil {
		n, e := Cents(text(m["balance"]))
		if e != nil {
			return c, e
		}
		c.Balance = Money(n)
		c.BalanceAt = time.Now()
	}
	for _, key := range []string{"created_at", "create_time"} {
		s := text(m[key])
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
			if t, e := time.ParseInLocation(layout, s, time.FixedZone("CST", 8*3600)); e == nil {
				c.CreatedAt = t
				break
			}
		}
		if !c.CreatedAt.IsZero() {
			break
		}
	}
	return c, nil
}
func PrivateFrom(m map[string]any) (PrivateCard, error) {
	p := PrivateCard{Number: strings.ReplaceAll(text(m["card_no"]), " ", ""), CVV: text(m["cvv"])}
	exp := text(m["expire_at_str"])
	for _, layout := range []string{"01/2006", "01/06", "2006-01-02", "2006-01", "2006-01-02 15:04:05"} {
		if t, e := time.Parse(layout, exp); e == nil {
			p.Month = int(t.Month())
			p.Year = t.Year()
			if layout == "01/06" {
				p.Year = 2000 + (p.Year % 100)
			}
			break
		}
	}
	if p.Month == 0 {
		return p, fmt.Errorf("环球卡片有效期格式未识别")
	}
	return p, nil
}
func Object(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("环球响应数据不是对象")
	}
	return m, nil
}
func String(v any) string { return text(v) }
