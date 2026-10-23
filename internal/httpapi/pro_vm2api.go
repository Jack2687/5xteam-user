package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/vm2api"
)

// Existing vm2api accounts keep their connection and runtime after settings change.
func (s *Server) proVMTarget(p model.MailAccountProfile) (model.DownstreamTarget, error) {
	if p.VM2 == nil || p.VM2.ConnectionID == "" {
		return model.DownstreamTarget{}, errors.New("Pro 账号缺少 vm2api 绑定")
	}
	t, err := s.store.DownstreamTarget(p.VM2.ConnectionID)
	if err != nil {
		return t, err
	}
	if t.Provider != "vm2api" {
		return t, errors.New("Pro vm2api 连接归属不匹配")
	}
	if cfg, key, e := s.store.ProVM2APISettings(); e == nil && cfg.URL == t.VM2API.URL && key != "" {
		t.Secret = key
	}
	return t, nil
}

func (s *Server) proAccountPushSettings(p model.MailAccountProfile) (model.ProSettings, string, string, error) {
	v, password, key, err := s.store.ProSettings()
	if err == nil && p.ProSIWC != nil && p.ProSIWC.AccountID > 0 && p.ProSIWC.TargetID != "" {
		t, e := s.store.DownstreamTarget(p.ProSIWC.TargetID)
		if e != nil {
			return v, password, key, e
		}
		v.Provider, v.Sub2, password = "sub2", t.Sub2, t.Secret
		v.Sub2.AuthMode = "siwc"
		v.PostMergeGroupIDs = nil // Original site group IDs must never address the SIWC site.
		s.applySIWCQuotaInterval(&v)
		return v, password, key, nil
	}
	if err == nil && p.ProRestockTargetID != "" {
		t, e := s.store.DownstreamTarget(p.ProRestockTargetID)
		if e != nil {
			return v, password, key, e
		}
		// Group IDs belong to one downstream connection, not the global default.
		// Existing accounts stay pinned when an operator switches push settings.
		if v.Provider != t.Provider || strings.TrimRight(strings.TrimSpace(v.Sub2.URL), "/") != strings.TrimRight(strings.TrimSpace(t.Sub2.URL), "/") || !strings.EqualFold(strings.TrimSpace(v.Sub2.Email), strings.TrimSpace(t.Sub2.Email)) || v.Sub2.AuthMode != t.Sub2.AuthMode {
			v.PostMergeGroupIDs, v.PostMergeGroupNames = nil, nil
		}
		v.Provider, v.Sub2, password = t.Provider, t.Sub2, t.Secret
		// Match the normalized live settings: omitempty restores an empty
		// model list as nil, which otherwise changes the restock binding hash.
		if v.Sub2.Models == nil {
			v.Sub2.Models = []string{}
		}
		s.applySIWCQuotaInterval(&v)
		return v, password, key, nil
	}
	if err != nil || p.VM2 == nil {
		s.applySIWCQuotaInterval(&v)
		return v, password, key, err
	}
	t, err := s.proVMTarget(p)
	if err == nil {
		v.Provider, v.VM2API = "vm2api", t.VM2API
	}
	return v, password, key, err
}

func (s *Server) validateProAutoPush(p model.MailAccountProfile, v model.ProSettings, password string) error {
	if v.Provider == "vm2api" {
		var key string
		var err error
		if p.VM2 != nil {
			var t model.DownstreamTarget
			t, err = s.proVMTarget(p)
			v.VM2API, key = t.VM2API, t.Secret
		} else {
			_, key, err = s.store.ProVM2APISettings()
		}
		if err != nil {
			return err
		}
		return vm2api.Validate(v.VM2API, key != "")
	}
	if v.Provider != "sub2" || v.Sub2.URL == "" || v.Sub2.Email == "" || password == "" || len(v.Sub2.GroupIDs) == 0 {
		return errors.New("请先配置 Pro 的 Sub2 或 vm2api 推送连接")
	}
	return nil
}

func (s *Server) pinProVM(p model.MailAccountProfile) (model.MailAccountProfile, error) {
	s.downstreamMu.Lock()
	defer s.downstreamMu.Unlock()
	p, _, err := s.store.MailAccountCredential(p.Email)
	if err != nil || p.VM2 != nil {
		return p, err
	}
	v, _, _, err := s.store.ProSettings()
	if err != nil {
		return p, err
	}
	if v.Provider != "vm2api" {
		return p, errors.New("推送设置已变化，请重新推送")
	}
	cfg, key, err := s.store.ProVM2APISettings()
	if err != nil {
		return p, err
	}
	if err = vm2api.Validate(cfg, key != ""); err != nil {
		return p, err
	}
	id, err := s.store.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro", Provider: "vm2api", VM2API: cfg, Secret: key})
	if err != nil {
		return p, err
	}
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return p, err
	}
	assignment := "space-" + hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(assignment))
	return s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		p.VM2 = &model.ProVM2Binding{ConnectionID: id, AssignmentID: assignment, SlotID: fmt.Sprintf("space-%x", hash[:12]), RuntimeType: vm2api.RuntimeType(cfg), Stage: "creating"}
		p.PushProvider, p.PushStatus = "vm2api", "running"
		p.Sub2AccountID, p.Sub2AccountName, p.CPAAuthFileName = 0, "", ""
	})
}

func (s *Server) syncProVM(p model.MailAccountProfile, slot vm2api.Slot, cfg model.VM2APISettings) (model.MailAccountProfile, error) {
	if p.VM2 == nil || slot.VM.ID != p.VM2.SlotID || slot.VM.AssignmentID != p.VM2.AssignmentID || slot.VM.ExecutionMode != vm2api.ExecutionMode(cfg) || !vm2api.SlotGroupsMatch(cfg, slot) {
		return p, errors.New("vm2api 返回的 Pro 槽位归属不匹配")
	}
	now := time.Now()
	return s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		p.VM2.ContainerName, p.VM2.Running, p.VM2.Schedulable = slot.VM.ContainerName, slot.Container != nil && slot.Container.Running, slot.VM.Schedulable
		p.VM2.SyncedAt = &now
	})
}

func (s *Server) proVMStage(email, stage string) error {
	_, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.VM2.Stage, p.VM2.Error = stage, "" })
	return err
}

func (s *Server) pushProVM(ctx context.Context, p model.MailAccountProfile, creds model.MailAccountCredentials) (out model.MailAccountProfile, retErr error) {
	if p.VM2 != nil && (p.VM2.DeletedAt != nil || p.ProVM2CleanupPending() || p.VM2.Stage == "deleting" || p.VM2.Stage == "delete_failed") {
		return p, errors.New("该 Pro 槽位已清理或正在清理，不能重新授权或重复创建")
	}
	p, err := s.pinProVM(p)
	if err != nil {
		return p, err
	}
	defer func() {
		if retErr != nil {
			_, _ = s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
				p.PushStatus, p.ProLastError, p.VM2.Error = "failed", retErr.Error(), retErr.Error()
			})
		}
	}()
	t, err := s.proVMTarget(p)
	if err != nil {
		return p, err
	}
	c := vm2api.New(t.VM2API, t.Secret)
	if err = c.Check(ctx); err != nil {
		return p, err
	}
	wasReady := p.VM2.ReadyAt != nil
	var slot vm2api.Slot
	if wasReady {
		slot, err = c.Detail(ctx, p.VM2.SlotID, p.VM2.AssignmentID)
	} else {
		if err = s.proVMStage(p.Email, "creating"); err != nil {
			return p, err
		}
		slot, err = c.Create(ctx, p.Email, p.VM2.AssignmentID)
	}
	if err != nil {
		return p, err
	}
	p, err = s.syncProVM(p, slot, t.VM2API)
	if err != nil {
		return p, err
	}
	if !p.VM2.Running {
		return p, errors.New("Pro 槽位运行环境尚未开机")
	}
	if err = s.proVMStage(p.Email, "authorizing"); err != nil {
		return p, err
	}
	if err = c.Import(ctx, p.VM2.SlotID, p.VM2.AssignmentID, p.Email, p.OAuthAccountID, creds.AccessToken, creds.RefreshToken); err != nil {
		return p, err
	}
	if err = s.proVMStage(p.Email, "validating"); err != nil {
		return p, err
	}
	five, seven, err := c.Quota(ctx, p.VM2.SlotID, p.VM2.AssignmentID)
	if err != nil {
		return p, err
	}
	// Reauthorization preserves an operator's scheduling suspension.
	if !wasReady {
		if err = s.proVMStage(p.Email, "scheduling"); err != nil {
			return p, err
		}
		if err = c.Schedule(ctx, p.VM2.SlotID, p.VM2.AssignmentID, true); err != nil {
			return p, err
		}
	}
	slot, err = c.Detail(ctx, p.VM2.SlotID, p.VM2.AssignmentID)
	if err != nil {
		return p, err
	}
	p, err = s.syncProVM(p, slot, t.VM2API)
	if err != nil {
		return p, err
	}
	if !p.VM2.Running || (!wasReady && !p.VM2.Schedulable) {
		return p, errors.New("Pro 槽位未就绪")
	}
	now := time.Now()
	return s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		p.PushProvider, p.PushStatus, p.ProLastError = "vm2api", "completed", ""
		p.VM2.Stage, p.VM2.Error, p.VM2.ReadyAt = "ready", "", &now
		p.Quota5H, p.Quota7D, p.QuotaCheckedAt, p.QuotaStatus = five, seven, &now, "completed"
		if p.ProMigration != nil {
			p.ProMigration.Downstream = model.ProDownstream(model.ProSettings{Provider: "vm2api", VM2API: t.VM2API})
		}
	})
}

func (s *Server) proVMQuota(ctx context.Context, p model.MailAccountProfile) (*model.FreeQuotaWindow, *model.FreeQuotaWindow, error) {
	if p.VM2 == nil || p.VM2.DeletedAt != nil {
		return nil, nil, errors.New("Pro 槽位不存在或已清理")
	}
	t, err := s.proVMTarget(p)
	if err != nil {
		return nil, nil, err
	}
	c := vm2api.New(t.VM2API, t.Secret)
	slot, err := c.Detail(ctx, p.VM2.SlotID, p.VM2.AssignmentID)
	if err != nil {
		return nil, nil, err
	}
	if p, err = s.syncProVM(p, slot, t.VM2API); err != nil {
		return nil, nil, err
	}
	if !p.VM2.Running {
		return nil, nil, errors.New("Pro 槽位运行环境未运行")
	}
	return c.Quota(ctx, p.VM2.SlotID, p.VM2.AssignmentID)
}

// A failure retains the binding for a cleanup-only retry. DELETE immediately
// removes the slot and its runtime; quota reads never suspend or stop it.
func (s *Server) deleteProVM(ctx context.Context, p model.MailAccountProfile) (model.MailAccountProfile, error) {
	if p.VM2 == nil || p.VM2.DeletedAt != nil {
		return p, nil
	}
	t, err := s.proVMTarget(p)
	if err != nil {
		return p, err
	}
	if err = s.proVMStage(p.Email, "deleting"); err != nil {
		return p, err
	}
	s.auditProEvent(p, "vm2api_cleanup", "running", "vm2api", "删除 Pro 槽位及运行环境", nil)
	err = vm2api.New(t.VM2API, t.Secret).Delete(ctx, p.VM2.SlotID, p.VM2.AssignmentID)
	now := time.Now()
	out, saveErr := s.store.UpdateProAccount(p.Email, func(p *model.MailAccountProfile) {
		if err != nil {
			p.VM2.Stage, p.VM2.Error, p.ProLastError = "delete_failed", err.Error(), err.Error()
			return
		}
		p.VM2.Stage, p.VM2.Error, p.ProLastError = "deleted", "", ""
		p.VM2.DeletedAt, p.VM2.SyncedAt = &now, &now
		p.VM2.Running, p.VM2.Schedulable = false, false
		p.PushStatus = "removed"
		if p.RemoveStatus == "team_removed" {
			p.RemoveStatus = "completed"
		}
	})
	status, message := "completed", "Pro 槽位及运行环境已删除"
	if err != nil {
		status, message = "failed", "Pro 槽位删除失败，可重试清理"
	}
	s.auditProEvent(p, "vm2api_cleanup", status, "vm2api", message, nil)
	return out, errors.Join(err, saveErr)
}

func (s *Server) finishProDownstream(ctx context.Context, p model.MailAccountProfile) (model.MailAccountProfile, error) {
	if p.ProVM2CleanupPending() {
		return s.deleteProVM(ctx, p)
	}
	return s.finishProPostMergeGroups(ctx, p)
}

func (s *Server) proVMConnection(w http.ResponseWriter, r *http.Request) (model.VM2APISettings, string, bool) {
	v, key, err := s.store.ProVM2APISettings()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return v, "", false
	}
	var input struct {
		VM2API json.RawMessage `json:"vm2api"`
		Key    string          `json:"vm2api_key"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err = decodeJSON(w, r, &input, 1<<20); err != nil {
			return v, "", false
		}
		if len(input.VM2API) > 0 {
			if err = json.Unmarshal(input.VM2API, &v); err != nil {
				writeAPI(w, 400, nil, "vm2api 设置无效")
				return v, "", false
			}
		}
		if value := strings.TrimSpace(input.Key); value != "" {
			key = value
		}
	}
	v.URL = strings.TrimRight(strings.TrimSpace(v.URL), "/")
	if err = vm2api.Validate(v, key != ""); err != nil {
		writeAPI(w, 400, nil, err.Error())
		return v, "", false
	}
	return v, key, true
}

func (s *Server) testProVM(w http.ResponseWriter, r *http.Request) {
	v, key, ok := s.proVMConnection(w, r)
	if !ok {
		return
	}
	if err := vm2api.New(v, key).Check(r.Context()); err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"connected": true}, "")
}

func (s *Server) getProVMProxies(w http.ResponseWriter, r *http.Request) {
	v, key, ok := s.proVMConnection(w, r)
	if !ok {
		return
	}
	proxies, err := vm2api.New(v, key).Proxies(r.Context())
	if err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, proxies, "")
}

func (s *Server) syncProVMAfterOAuth(ctx context.Context, email string) error {
	p, _, err := s.store.MailAccountCredential(email)
	if err != nil {
		return err
	}
	if p.ManagementScope != "pro" || p.VM2 == nil || p.VM2.ReadyAt == nil || p.VM2.DeletedAt != nil {
		return nil
	}
	unlock := s.lockProAccount(email)
	defer unlock()
	p, credentials, err := s.store.MailAccountCredential(email)
	if err != nil {
		return err
	}
	if p.VM2 == nil || p.VM2.DeletedAt != nil || p.ProVM2CleanupPending() {
		return nil
	}
	_, err = s.pushProVM(ctx, p, credentials)
	return err
}
