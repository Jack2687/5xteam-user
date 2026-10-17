package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Bot struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	Webhook        string `json:"webhook"`
	Secret         string `json:"secret"`
	WebhookPresent bool   `json:"webhook_present"`
	SecretPresent  bool   `json:"secret_present"`
	ClearSecret    bool   `json:"clear_secret,omitempty"`
}
type Settings struct {
	Enabled     bool  `json:"enabled"`
	Restock     bool  `json:"restock"`
	MergeStart  bool  `json:"merge_start"`
	MergeResult bool  `json:"merge_result"`
	Bots        []Bot `json:"bots"`
}

func Defaults() Settings {
	return Settings{Restock: true, MergeStart: true, MergeResult: true, Bots: []Bot{}}
}
func (s Settings) Public() Settings {
	out := s
	out.Bots = append([]Bot{}, s.Bots...)
	for i := range out.Bots {
		b := &out.Bots[i]
		b.WebhookPresent = b.Webhook != ""
		b.SecretPresent = b.Secret != ""
		b.Webhook = ""
		b.Secret = ""
		b.ClearSecret = false
	}
	return out
}
func (s Settings) Accepts(kind string) bool {
	return s.Enabled && (kind == "restock" && s.Restock || kind == "merge_start" && s.MergeStart || kind == "merge_result" && s.MergeResult)
}

var hookPath = regexp.MustCompile(`^/open-apis/bot/v2/hook/[A-Za-z0-9_-]+$`)

func ValidateWebhook(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || (u.Host != "open.feishu.cn" && u.Host != "open.larksuite.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !hookPath.MatchString(u.Path) {
		return errors.New("请填写飞书自定义机器人的 HTTPS Webhook 地址")
	}
	return nil
}
func MergeSettings(in, previous Settings) (Settings, error) {
	if len(in.Bots) > 20 {
		return in, errors.New("最多配置 20 个飞书机器人")
	}
	old := map[string]Bot{}
	for _, b := range previous.Bots {
		old[b.ID] = b
	}
	seen := map[string]bool{}
	for i := range in.Bots {
		b := &in.Bots[i]
		b.ID = strings.TrimSpace(b.ID)
		b.Name = strings.TrimSpace(b.Name)
		b.Webhook = strings.TrimSpace(b.Webhook)
		b.Secret = strings.TrimSpace(b.Secret)
		if b.ID == "" || len(b.ID) > 100 || seen[b.ID] || b.Name == "" || len([]rune(b.Name)) > 60 {
			return in, errors.New("机器人名称不能为空，标识不能重复")
		}
		seen[b.ID] = true
		if b.Webhook == "" {
			b.Webhook = old[b.ID].Webhook
		}
		if b.ClearSecret {
			b.Secret = ""
		} else if b.Secret == "" {
			b.Secret = old[b.ID].Secret
		}
		b.ClearSecret = false
		if err := ValidateWebhook(b.Webhook); err != nil {
			return in, err
		}
		if len(b.Secret) > 512 {
			return in, errors.New("签名密钥过长")
		}
	}
	if in.Bots == nil {
		in.Bots = []Bot{}
	}
	return in, nil
}

// Feishu signs an empty message using timestamp + newline + secret as the key,
// matching transit-hub's bot implementation (different from DingTalk signing).
func Sign(timestamp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(timestamp, 10)+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
func Send(ctx context.Context, client *http.Client, bot Bot, text string) error {
	if err := ValidateWebhook(bot.Webhook); err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Never forward a bot credential through redirects.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	body := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	if bot.Secret != "" {
		timestamp := time.Now().Unix()
		body["timestamp"] = strconv.FormatInt(timestamp, 10)
		body["sign"] = Sign(timestamp, bot.Secret)
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bot.Webhook, bytes.NewReader(raw))
	if err != nil {
		return errors.New("创建飞书通知失败")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("飞书连接失败或超时，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("飞书返回 HTTP %d", resp.StatusCode)
	}
	var result struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return errors.New("飞书返回格式无效")
	}
	code := result.Code
	if code == nil {
		code = result.StatusCode
	}
	if code == nil {
		return errors.New("飞书未返回发送结果")
	}
	if *code != 0 {
		return fmt.Errorf("飞书拒绝通知（错误码 %d），请检查机器人签名、关键词和 IP 设置", *code)
	}
	return nil
}
