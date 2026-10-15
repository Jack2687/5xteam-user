package ejh

import (
	"bytes"
	"context"
	"crypto/des"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DES/ECB/PKCS5 is mandated only for the supplier wire protocol, not local storage.
func Encrypt(param []byte, secret string) (string, error) {
	if len([]byte(secret)) < 8 {
		return "", fmt.Errorf("AppSecret 至少需要 8 字节")
	}
	b, e := des.NewCipher([]byte(secret)[:8])
	if e != nil {
		return "", e
	}
	pad := 8 - len(param)%8
	plain := append(append([]byte{}, param...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(plain))
	for i := 0; i < len(plain); i += 8 {
		b.Encrypt(out[i:i+8], plain[i:i+8])
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string { return "EJH " + e.Code + ": " + e.Message }

type Client struct {
	HTTP         *http.Client
	mu           sync.Mutex
	token, scope string
	expires      time.Time
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) post(ctx context.Context, address, token string, body any) (json.RawMessage, error) {
	raw, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	res, e := c.HTTP.Do(r)
	if e != nil {
		return nil, fmt.Errorf("卡台网络请求未确认（可能已处理），请先核对结果")
	}
	defer res.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if e != nil {
		return nil, fmt.Errorf("卡台响应读取失败，结果待核对")
	}
	if len(raw) > 8<<20 {
		return nil, fmt.Errorf("卡台响应超出大小限制，结果待核对")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("卡台 HTTP %d，结果待核对", res.StatusCode)
	}
	var env struct {
		Code json.RawMessage `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil, fmt.Errorf("卡台响应格式异常，结果待核对")
	}
	code := strings.Trim(string(env.Code), "\"")
	if code != "000000" {
		if len(code) > 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return nil, fmt.Errorf("卡台业务代码格式异常，结果待核对")
		}
		return nil, &APIError{Code: code, Message: knownMessage(code)}
	}
	data := bytes.TrimSpace(env.Data)
	// Some supplier responses encode data as a JSON string instead of an object.
	// Normalize it here so authentication and every business method share support.
	if len(data) > 0 && data[0] == '"' {
		var inner string
		if json.Unmarshal(data, &inner) != nil {
			return nil, fmt.Errorf("卡台业务结果字符串解析失败，结果待核对")
		}
		data = bytes.TrimSpace([]byte(inner))
	}
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, fmt.Errorf("卡台缺少业务结果，结果待核对")
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("卡台业务结果不是有效 JSON，结果待核对")
	}
	return json.RawMessage(data), nil
}
func knownMessage(code string) string {
	m := map[string]string{"000007": "验签失败", "000008": "参数缺失", "100004": "开卡余额不足", "100012": "不支持此结算币种", "100018": "卡类型权限未开通", "100022": "开卡过于频繁，请稍后核对", "100023": "开卡超过最大金额", "100024": "开卡金额过小", "300003": "存在未清算授权，无法销卡", "800003": "账户可用余额不足"}
	if s := m[code]; s != "" {
		return s
	}
	return "供应商返回业务错误，请凭响应码核查（不输出敏感原始报文）"
}
func (c *Client) Token(ctx context.Context, cfg Settings, secret string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := sha256.Sum256([]byte(cfg.URL + "|" + cfg.AppKey + "|" + secret))
	scope := fmt.Sprintf("%x", sum)
	if c.scope == scope && c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	if cfg.AppKey == "" || secret == "" {
		return "", fmt.Errorf("请先保存 AppKey 和 AppSecret")
	}
	raw, e := c.post(ctx, cfg.URL+"/api/v3/authenticate", "", map[string]string{"appKey": cfg.AppKey})
	if e != nil {
		return "", e
	}
	var v struct {
		Token    string          `json:"authToken"`
		Validity json.RawMessage `json:"validity"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", fmt.Errorf("认证响应格式异常，无法解析认证结果")
	}
	if v.Token == "" {
		return "", fmt.Errorf("认证未返回 authToken")
	}
	ms, _ := strconv.ParseInt(strings.Trim(string(v.Validity), "\""), 10, 64)
	// validity is a duration in milliseconds. Clamp before duration conversion;
	// malformed/expired values are not cached, and cannot overflow.
	ttl := time.Duration(0)
	if ms > 0 {
		ttl = time.Duration(min(ms, int64((24*time.Hour)/time.Millisecond))) * time.Millisecond
	}
	margin := min(30*time.Second, ttl/10)
	c.scope, c.token, c.expires = scope, v.Token, time.Now().Add(ttl-margin)
	return v.Token, nil
}
func (c *Client) Call(ctx context.Context, cfg Settings, secret, method string, data any, out any) error {
	token, e := c.Token(ctx, cfg, secret)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(map[string]any{"method": method, "timestamp": time.Now().Unix(), "v": "3.0", "data": data})
	if e != nil {
		return e
	}
	encrypted, e := Encrypt(raw, secret)
	if e != nil {
		return e
	}
	raw, e = c.post(ctx, cfg.URL+"/api/v3/card", token, map[string]string{"appKey": cfg.AppKey, "param": encrypted})
	if e != nil {
		return e
	}
	if out == nil {
		return nil
	}
	if e = json.Unmarshal(raw, out); e != nil {
		return fmt.Errorf("卡台业务响应无法解析，结果待核对")
	}
	return nil
}
