package vm2api

import (
	"bytes"
	"chatgpt-space-merge/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	HTTP     *http.Client
	Settings model.VM2APISettings
	Key      string
}
type APIError struct {
	Status         int
	Code           string
	Message        string
	UpstreamStatus int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("vm2api %s（HTTP %d）: %s", e.Code, e.Status, e.Message)
}
func UpstreamUnauthorized(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.UpstreamStatus == 401
}
func New(v model.VM2APISettings, key string) *Client {
	timeout := 7 * time.Minute
	if RuntimeType(v) == "kvm" {
		timeout = 25 * time.Minute
	}
	return &Client{Settings: v, Key: key, HTTP: &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func RuntimeType(v model.VM2APISettings) string {
	if v.RuntimeType == "" {
		return "docker"
	}
	return v.RuntimeType
}

func ExecutionMode(v model.VM2APISettings) string { return RuntimeType(v) + "_per_slot" }

func Validate(v model.VM2APISettings, keyPresent bool) error {
	if len(v.GroupIDs) > 100 {
		return errors.New("vm2api 分组最多选择 100 个")
	}
	for _, id := range v.GroupIDs {
		if !groupIDPattern.MatchString(strings.TrimSpace(id)) {
			return errors.New("vm2api 分组 ID 无效")
		}
	}
	u, e := url.Parse(v.URL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("vm2api 地址必须是 http(s) 服务根地址")
	}
	if !keyPresent {
		return errors.New("vm2api 需要管理密钥（不是模型调用 Key）")
	}
	if !oneOf(RuntimeType(v), "docker", "kvm") {
		return errors.New("vm2api 形态必须为 Docker 或 KVM")
	}
	if RuntimeType(v) == "kvm" && (v.VCPUs < 1 || v.VCPUs > 16 || v.DiskGB < 10 || v.DiskGB > 200) {
		return errors.New("KVM 需要 1–16 vCPU、10–200 GB 磁盘")
	}
	if !oneOf(v.Kernel, "ubuntu-24.04", "debian-12", "archlinux", "fedora-41") || !oneOf(v.Memory, "256m", "512m", "1g", "2g", "4g", "8g", "16g") || !oneOf(v.Locale, "en_US.UTF-8", "zh_CN.UTF-8", "ja_JP.UTF-8", "C") {
		return errors.New("vm2api 系统、内存或语言无效")
	}
	if v.ReloginFailureLimit < 0 || v.ReloginFailureLimit > 20 || v.QuotaRemainingThresholdPercent < 0 || v.QuotaRemainingThresholdPercent > 100 {
		return errors.New("vm2api 重登次数或额度阈值无效")
	}
	if v.StatusCheckIntervalSeconds < 10 || v.QuotaCheckIntervalSeconds < 10 {
		return errors.New("vm2api 检查间隔至少 10 秒")
	}
	return nil
}
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func (c *Client) request(ctx context.Context, method, path, assignment string, body, out any) error {
	var input io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		input = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Settings.URL, "/")+"/api/panel"+path, input)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+c.Key)
	r.Header.Set("Content-Type", "application/json")
	if assignment != "" {
		r.Header.Set("X-Space-Assignment", assignment)
	}
	resp, e := c.HTTP.Do(r)
	if e != nil {
		return fmt.Errorf("vm2api 请求失败: %w", e)
	}
	defer resp.Body.Close()
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				UpstreamStatus int `json:"upstream_status"`
			} `json:"details"`
		} `json:"error"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); e != nil {
		return fmt.Errorf("vm2api 响应无效（HTTP %d）", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.OK {
		message := strings.ReplaceAll(envelope.Error.Message, c.Key, "[redacted]")
		return &APIError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: message, UpstreamStatus: envelope.Error.Details.UpstreamStatus}
	}
	if out != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}

type Slot struct {
	VM struct {
		ID            string   `json:"id"`
		AssignmentID  string   `json:"assignment_id"`
		ExecutionMode string   `json:"execution_mode"`
		ContainerName string   `json:"container_name"`
		PoolID        string   `json:"pool_id"`
		GroupIDs      []string `json:"group_ids"`
		Schedulable   bool     `json:"schedulable"`
		Status        string   `json:"status"`
	} `json:"vm"`
	Container *struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Running bool   `json:"running"`
	} `json:"container"`
}

func (c *Client) Check(ctx context.Context) error {
	var caps struct {
		Version        int  `json:"version"`
		Docker         bool `json:"docker_per_slot"`
		KVM            bool `json:"kvm_per_slot"`
		Delete         bool `json:"delete_account"`
		ProxySelection bool `json:"proxy_selection"`
		AccountGroups  bool `json:"account_groups"`
	}
	if e := c.request(ctx, "GET", "/space-capabilities", "", nil, &caps); e != nil {
		return e
	}
	if caps.Version < 1 || !caps.Docker || !caps.Delete {
		return errors.New("vm2api 未安装 Space 一槽一容器接口")
	}
	if RuntimeType(c.Settings) == "kvm" && !caps.KVM {
		return errors.New("vm2api 尚不支持一槽一 KVM 独立 Codex 内核，请先更新 vm2api")
	}
	if c.Settings.BindProxy && len(NormalizeProxyIDs(c.Settings.ProxyIDs)) > 0 && !caps.ProxySelection {
		return errors.New("vm2api 尚不支持代理选择，请先更新 vm2api 后再推送")
	}
	if len(c.Settings.GroupIDs) > 0 && !caps.AccountGroups {
		return errors.New("vm2api 尚不支持多选账号分组，请先更新 vm2api 后再推送")
	}
	return nil
}
func (c *Client) Create(ctx context.Context, email, assignment string) (Slot, error) {
	var out Slot
	machine := map[string]any{"memory": c.Settings.Memory}
	if RuntimeType(c.Settings) == "kvm" {
		machine["vcpus"], machine["disk_gb"] = c.Settings.VCPUs, c.Settings.DiskGB
	}
	body := map[string]any{"assignment_id": assignment, "account_email": email, "execution_mode": ExecutionMode(c.Settings), "platform": "openai", "runtime_type": RuntimeType(c.Settings), "proxy_id": "px-local", "pool_id": c.Settings.PoolID, "kernel": c.Settings.Kernel, "locale": c.Settings.Locale, "region": c.Settings.Region, "machine": machine, "start": true}
	if len(c.Settings.GroupIDs) > 0 {
		body["group_ids"] = NormalizeGroupIDs(c.Settings.GroupIDs)
	}
	if ids := NormalizeProxyIDs(c.Settings.ProxyIDs); c.Settings.BindProxy && len(ids) > 0 {
		// vm2api reads counts, chooses the least-bound exit and reserves it before
		// starting Docker. The assignment pins that choice across ambiguous retries.
		body["proxy_ids"] = ids
	}
	e := c.request(ctx, "POST", "/vms/create", assignment, body, &out)
	return out, e
}
func (c *Client) Detail(ctx context.Context, id, assignment string) (Slot, error) {
	var out Slot
	e := c.request(ctx, "GET", "/vms/"+url.PathEscape(id), assignment, nil, &out)
	return out, e
}
func (c *Client) Import(ctx context.Context, id, assignment, email, account, at, rt string) error {
	return c.request(ctx, "POST", "/vms/import", assignment, map[string]any{"vm_id": id, "email": email, "chatgpt_account_id": account, "access_token": at, "refresh_token": rt}, nil)
}
func (c *Client) Schedule(ctx context.Context, id, assignment string, on bool) error {
	return c.request(ctx, "POST", "/vms/"+url.PathEscape(id)+"/schedulable", assignment, map[string]bool{"schedulable": on}, nil)
}
func (c *Client) Delete(ctx context.Context, id, assignment string) error {
	return c.request(ctx, "DELETE", "/vms/"+url.PathEscape(id), assignment, nil, nil)
}
func (c *Client) Quota(ctx context.Context, id, assignment string) (*model.FreeQuotaWindow, *model.FreeQuotaWindow, error) {
	var out struct {
		Windows []struct {
			ID         string          `json:"id"`
			Used       *float64        `json:"used_percent"`
			ResetAt    json.RawMessage `json:"reset_at"`
			ResetAfter int64           `json:"reset_after_seconds"`
			Minutes    int64           `json:"window_minutes"`
		} `json:"windows"`
	}
	e := c.request(ctx, "POST", "/vms/"+url.PathEscape(id)+"/openai-quota/refresh", assignment, map[string]any{}, &out)
	if e != nil {
		return nil, nil, e
	}
	var five, seven *model.FreeQuotaWindow
	for _, w := range out.Windows {
		if w.Used == nil || w.Minutes <= 0 || *w.Used < 0 || *w.Used > 100 {
			continue
		}
		v := &model.FreeQuotaWindow{UsedPercent: *w.Used, LimitWindowSeconds: w.Minutes * 60, ResetAfterSeconds: w.ResetAfter}
		if json.Unmarshal(w.ResetAt, &v.ResetAt) != nil {
			var date string
			if json.Unmarshal(w.ResetAt, &date) == nil {
				if t, e := time.Parse(time.RFC3339, date); e == nil {
					v.ResetAt = t.Unix()
				}
			}
		}
		if w.ID == "5h" {
			five = v
		}
		if w.ID == "7d" {
			seven = v
		}
	}
	if five == nil && seven == nil {
		return nil, nil, errors.New("vm2api 未返回有效额度窗口")
	}
	return five, seven, nil
}
