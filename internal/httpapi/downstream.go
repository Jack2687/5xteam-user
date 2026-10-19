package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
	"chatgpt-space-merge/internal/vm2api"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func accountProvider(p model.FreeAccountProfile) string {
	if p.PushProvider == "sub2" || p.PushProvider == "cpa" || p.PushProvider == "vm2api" {
		return p.PushProvider
	}
	if p.VM2AssignmentID != "" || p.PushProvider == "vm2api" {
		return "vm2api"
	}
	if p.CPAAuthFileName != "" || p.PushProvider == "cpa" {
		return "cpa"
	}
	if p.Sub2AccountID > 0 || p.PushProvider == "sub2" {
		return "sub2"
	}
	return ""
}
func (s *Server) downstreamTarget(p model.FreeAccountProfile) (model.DownstreamTarget, error) {
	s.downstreamMu.Lock()
	defer s.downstreamMu.Unlock()
	return s.downstreamTargetLocked(p)
}
func (s *Server) downstreamTargetLocked(p model.FreeAccountProfile, pin ...bool) (model.DownstreamTarget, error) {
	// Re-read after taking the settings lock; concurrent pushes share a cycle binding.
	fresh, _, err := s.store.FreeAccountCredential(p.ID)
	if err != nil {
		return model.DownstreamTarget{}, err
	}
	if fresh.CycleID != p.CycleID {
		return model.DownstreamTarget{}, store.ErrStaleCycle
	}
	p = fresh
	if p.DownstreamConnectionID != "" {
		t, e := s.store.DownstreamTarget(p.DownstreamConnectionID)
		if e != nil {
			return t, e
		}
		// Editing policy or rotating a key for the same connection applies now;
		// switching connections leaves the old target intact.
		switch t.Provider {
		case "sub2":
			if v, key, e := s.store.Sub2Settings(); e == nil && v.URL == t.Sub2.URL && v.Email == t.Sub2.Email {
				t.Secret = key
				t.Sub2.Enable401Check = v.Enable401Check
				t.Sub2.QuotaEnabled = v.QuotaEnabled
				t.Sub2.StatusCheckIntervalSeconds = v.StatusCheckIntervalSeconds
				t.Sub2.QuotaCheckIntervalSeconds = v.QuotaCheckIntervalSeconds
				t.Sub2.ReloginFailureLimit = v.ReloginFailureLimit
				t.Sub2.QuotaRemainingThresholdPercent = v.QuotaRemainingThresholdPercent
				t.Sub2.SchedulingPauseTimeoutSeconds = v.SchedulingPauseTimeoutSeconds
			}
		case "cpa":
			if v, key, e := s.store.CPASettings(); e == nil && v.URL == t.CPA.URL {
				t.Secret = key
				t.CPA.Enable401Check = v.Enable401Check
				t.CPA.QuotaEnabled = v.QuotaEnabled
				t.CPA.StatusCheckIntervalSeconds = v.StatusCheckIntervalSeconds
				t.CPA.QuotaCheckIntervalSeconds = v.QuotaCheckIntervalSeconds
				t.CPA.ReloginFailureLimit = v.ReloginFailureLimit
				t.CPA.QuotaRemainingThresholdPercent = v.QuotaRemainingThresholdPercent
			}
		case "vm2api":
			if v, key, e := s.store.VM2APISettings(); e == nil && v.URL == t.VM2API.URL {
				t.Secret = key
				t.VM2API.Enable401Check = v.Enable401Check
				t.VM2API.QuotaEnabled = v.QuotaEnabled
				t.VM2API.StatusCheckIntervalSeconds = v.StatusCheckIntervalSeconds
				t.VM2API.QuotaCheckIntervalSeconds = v.QuotaCheckIntervalSeconds
				t.VM2API.ReloginFailureLimit = v.ReloginFailureLimit
				t.VM2API.QuotaRemainingThresholdPercent = v.QuotaRemainingThresholdPercent
			}
		}
		return t, nil
	}
	sub, secret, err := s.store.Sub2Settings()
	if err != nil {
		return model.DownstreamTarget{}, err
	}
	provider := accountProvider(p)
	if provider == "" {
		provider = providerForSettings(sub)
	}
	t := model.DownstreamTarget{Provider: provider, Sub2: sub, Secret: secret}
	t.Sub2.Provider = provider
	switch provider {
	case "cpa":
		t.CPA, t.Secret, err = s.store.CPASettings()
	case "vm2api":
		t.VM2API, t.Secret, err = s.store.VM2APISettings()
	}
	if err != nil {
		return t, err
	}
	if accountProvider(p) == "" && p.PushStatus != "running" && p.PushStatus != "completed" && (len(pin) == 0 || !pin[0]) {
		return t, nil
	}
	id, err := s.store.SaveDownstreamTarget(t)
	if err != nil {
		return t, err
	}
	_, err = s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) { v.DownstreamConnectionID = id; v.PushProvider = provider })
	return t, err
}

func (s *Server) bindPushSettings(p model.FreeAccountProfile, creds store.FreeAccountCredentials) (model.Sub2Settings, string, error) {
	if p.DownstreamConnectionID == "" && accountProvider(p) == "" {
		if p.OAuthStatus != "completed" || creds.OAuthAccessToken == "" || creds.OAuthRefreshToken == "" {
			return model.Sub2Settings{}, "", errors.New("请先取得完整的 Codex AT/RT")
		}
		s.downstreamMu.Lock()
		t, e := s.downstreamTargetLocked(p)
		if e == nil {
			switch t.Provider {
			case "vm2api":
				e = vm2api.Validate(t.VM2API, t.Secret != "")
			case "cpa":
				if t.CPA.URL == "" || t.Secret == "" {
					e = errors.New("请先配置 CPA 地址和管理密钥")
				}
			default:
				e = validateSub2Connection(t.Sub2.URL, t.Sub2.Email, t.Secret)
			}
		}
		if e == nil {
			_, e = s.downstreamTargetLocked(p, true)
		}
		s.downstreamMu.Unlock()
		if e != nil {
			return model.Sub2Settings{}, "", e
		}
	}
	return s.freePushSettings(p)
}
func (s *Server) pinExistingDownstreamsLocked() error {
	for _, p := range s.store.FreeAccounts() {
		if p.DownstreamConnectionID == "" && !p.DownstreamCleaned && (accountProvider(p) != "" || p.PushStatus == "running" || p.PushStatus == "completed") {
			if _, e := s.downstreamTargetLocked(p); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Server) freePushSettings(p model.FreeAccountProfile) (model.Sub2Settings, string, error) {
	t, e := s.downstreamTarget(p)
	v := t.Sub2
	v.Provider = t.Provider
	if t.Provider == "cpa" {
		v.Enable401Check = t.CPA.Enable401Check
		v.QuotaEnabled = t.CPA.QuotaEnabled
		v.StatusCheckIntervalSeconds = t.CPA.StatusCheckIntervalSeconds
		v.QuotaCheckIntervalSeconds = t.CPA.QuotaCheckIntervalSeconds
		v.ReloginFailureLimit = t.CPA.ReloginFailureLimit
		v.QuotaRemainingThresholdPercent = t.CPA.QuotaRemainingThresholdPercent
	}
	if t.Provider == "vm2api" {
		v.Enable401Check = t.VM2API.Enable401Check
		v.QuotaEnabled = t.VM2API.QuotaEnabled
		v.StatusCheckIntervalSeconds = t.VM2API.StatusCheckIntervalSeconds
		v.QuotaCheckIntervalSeconds = t.VM2API.QuotaCheckIntervalSeconds
		v.ReloginFailureLimit = t.VM2API.ReloginFailureLimit
		v.QuotaRemainingThresholdPercent = t.VM2API.QuotaRemainingThresholdPercent
	}
	return v, t.Secret, e
}
func (s *Server) freeCPASettings(p model.FreeAccountProfile) (model.CPASettings, string, error) {
	t, e := s.downstreamTarget(p)
	return t.CPA, t.Secret, e
}
func (s *Server) vmClient(p model.FreeAccountProfile) (*vm2api.Client, error) {
	t, e := s.downstreamTarget(p)
	if e != nil {
		return nil, e
	}
	if t.Provider != "vm2api" {
		return nil, errors.New("账号没有绑定 vm2api")
	}
	return vm2api.New(t.VM2API, t.Secret), nil
}
func (s *Server) setVMStage(p model.FreeAccountProfile, stage string) error {
	_, e := s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) { v.VM2Stage = stage })
	return e
}
func (s *Server) syncVMSlot(p model.FreeAccountProfile, slot vm2api.Slot) error {
	target, err := s.downstreamTarget(p)
	if err != nil {
		return err
	}
	if slot.VM.ID != p.VM2SlotID || slot.VM.AssignmentID != p.VM2AssignmentID || slot.VM.ExecutionMode != vm2api.ExecutionMode(target.VM2API) {
		return errors.New("vm2api 返回的槽位归属不匹配")
	}
	now := time.Now()
	_, e := s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) {
		v.VM2ContainerName = slot.VM.ContainerName
		v.VM2RuntimeType = vm2api.RuntimeType(target.VM2API)
		v.VM2ContainerRunning = slot.Container != nil && slot.Container.Running
		v.VM2Schedulable = slot.VM.Schedulable
		v.VM2SyncedAt = &now
	})
	return e
}
func (s *Server) pushVMAccount(ctx context.Context, p model.FreeAccountProfile, creds store.FreeAccountCredentials) (model.FreeAccountProfile, error) {
	client, e := s.vmClient(p)
	if e != nil {
		return p, e
	}
	if e = vm2api.Validate(client.Settings, client.Key != ""); e != nil {
		return p, e
	}
	if p.OAuthStatus != "completed" || creds.OAuthAccessToken == "" || creds.OAuthRefreshToken == "" {
		return p, errors.New("请先取得完整的 Codex AT/RT")
	}
	if p.VM2AssignmentID == "" {
		b := make([]byte, 24)
		if _, e = rand.Read(b); e != nil {
			return p, e
		}
		assignment := "space-" + hex.EncodeToString(b)
		hash := sha256.Sum256([]byte(assignment))
		p, e = s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) {
			v.VM2AssignmentID = assignment
			v.VM2SlotID = fmt.Sprintf("space-%x", hash[:12])
			v.VM2Stage = "creating"
			v.Status = "pushing"
			v.PushStatus = "running"
			v.LastError = ""
		})
		if e != nil {
			return p, e
		}
	}
	if p.PushStatus == "completed" {
		return p, nil
	}
	if e = client.Check(ctx); e != nil {
		return p, e
	}
	if e = s.setVMStage(p, "creating"); e != nil {
		return p, e
	}
	slot, e := client.Create(ctx, p.Email, p.VM2AssignmentID)
	if e != nil {
		return p, e
	}
	if e = s.syncVMSlot(p, slot); e != nil {
		return p, e
	}
	if slot.Container == nil || !slot.Container.Running {
		return p, errors.New("vm2api 槽位运行环境尚未开机")
	}
	if e = s.setVMStage(p, "authorizing"); e != nil {
		return p, e
	}
	if e = client.Import(ctx, p.VM2SlotID, p.VM2AssignmentID, p.Email, p.OAuthAccountID, creds.OAuthAccessToken, creds.OAuthRefreshToken); e != nil {
		return p, e
	}
	if e = s.setVMStage(p, "validating"); e != nil {
		return p, e
	}
	five, seven, e := client.Quota(ctx, p.VM2SlotID, p.VM2AssignmentID)
	if e != nil {
		return p, e
	}
	if e = s.setVMStage(p, "scheduling"); e != nil {
		return p, e
	}
	if e = client.Schedule(ctx, p.VM2SlotID, p.VM2AssignmentID, true); e != nil {
		return p, e
	}
	slot, e = client.Detail(ctx, p.VM2SlotID, p.VM2AssignmentID)
	if e != nil {
		return p, e
	}
	if e = s.syncVMSlot(p, slot); e != nil {
		return p, e
	}
	if slot.Container == nil || !slot.Container.Running || !slot.VM.Schedulable || !vm2api.SlotGroupsMatch(client.Settings, slot) {
		return p, errors.New("vm2api 槽位运行环境或调度尚未就绪")
	}
	now := time.Now()
	return s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) {
		v.Status = "monitoring"
		v.PushStatus = "completed"
		v.VM2Stage = "ready"
		v.LastError = ""
		v.PushedAt = &now
		v.Quota5H = five
		v.Quota7D = seven
		v.QuotaCheckedAt = &now
		v.QuotaStatus = "completed"
	})
}
func (s *Server) reloginVMAccount(ctx context.Context, p model.FreeAccountProfile, creds store.FreeAccountCredentials) error {
	unlock := s.lockFreeAccountRemove(p.ID)
	defer unlock()
	current, _, readErr := s.store.FreeAccountCredential(p.ID)
	if readErr != nil {
		return readErr
	}
	if current.CycleID != p.CycleID || current.RemoteRemovedAt != nil || current.RemoveStatus == "completed" {
		return errors.New("账号已移出，不能重新授权")
	}
	c, e := s.vmClient(p)
	if e != nil {
		return e
	}
	if p.VM2SlotID == "" {
		return errors.New("账号没有绑定 vm2api 槽位")
	}
	if e = c.Import(ctx, p.VM2SlotID, p.VM2AssignmentID, p.Email, p.OAuthAccountID, creds.OAuthAccessToken, creds.OAuthRefreshToken); e != nil {
		return e
	}
	if _, _, e = c.Quota(ctx, p.VM2SlotID, p.VM2AssignmentID); e != nil {
		return e
	}
	slot, e := c.Detail(ctx, p.VM2SlotID, p.VM2AssignmentID)
	if e != nil {
		return e
	}
	if e = s.syncVMSlot(p, slot); e != nil {
		return e
	}
	now := time.Now()
	_, e = s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) {
		v.Status = "monitoring"
		v.OAuthStatus = "completed"
		v.PushStatus = "completed"
		v.QuotaStatus = "pending"
		v.VM2Stage = "ready"
		v.LastError = ""
		v.PushedAt = &now
		v.ReloginCount++
		v.StatusCheckedAt = nil
		clearReloginFailures(v)
	})
	return e
}
func (s *Server) vmQuota(ctx context.Context, p model.FreeAccountProfile) (*model.FreeQuotaWindow, *model.FreeQuotaWindow, error) {
	c, e := s.vmClient(p)
	if e != nil {
		return nil, nil, e
	}
	slot, e := c.Detail(ctx, p.VM2SlotID, p.VM2AssignmentID)
	if e != nil {
		return nil, nil, e
	}
	if e = s.syncVMSlot(p, slot); e != nil {
		return nil, nil, e
	}
	if slot.Container == nil || !slot.Container.Running {
		return nil, nil, errors.New("vm2api 账号运行环境未运行")
	}
	return c.Quota(ctx, p.VM2SlotID, p.VM2AssignmentID)
}
func (s *Server) deleteVMAccount(ctx context.Context, p model.FreeAccountProfile) error {
	if p.VM2SlotID == "" {
		return nil
	}
	c, e := s.vmClient(p)
	if e != nil {
		return e
	}
	if e = s.setVMStage(p, "deleting"); e != nil {
		return e
	}
	if e = c.Delete(ctx, p.VM2SlotID, p.VM2AssignmentID); e != nil {
		return e
	}
	_, e = s.store.UpdateFreeAccountCycle(p.ID, p.CycleID, func(v *model.FreeAccountProfile) {
		v.VM2Stage = "deleted"
		v.VM2ContainerRunning = false
		v.VM2Schedulable = false
	})
	return e
}
func (s *Server) testVMSettings(w http.ResponseWriter, r *http.Request) {
	v, key, e := s.store.VM2APISettings()
	if e != nil {
		writeAPI(w, 500, nil, e.Error())
		return
	}
	e = vm2api.New(v, key).Check(r.Context())
	if e != nil {
		writeAPI(w, 400, nil, e.Error())
		return
	}
	writeAPI(w, 200, map[string]any{"connected": true}, "")
}
func normalizeVMSettings(v *model.VM2APISettings) {
	v.URL = strings.TrimRight(strings.TrimSpace(v.URL), "/")
	v.ProxyIDs = vm2api.NormalizeProxyIDs(v.ProxyIDs)
	if v.GroupIDs == nil && v.PoolID != "" {
		v.GroupIDs = []string{v.PoolID}
	}
	v.GroupIDs = vm2api.NormalizeGroupIDs(v.GroupIDs)
	v.PoolID = ""
	if v.RuntimeType == "" {
		v.RuntimeType = "docker"
	}
	if v.VCPUs == 0 {
		v.VCPUs = 2
	}
	if v.DiskGB == 0 {
		v.DiskGB = 20
	}
	if v.Kernel == "" {
		v.Kernel = "ubuntu-24.04"
	}
	if v.Memory == "" {
		v.Memory = "1g"
	}
	if v.Locale == "" {
		v.Locale = "en_US.UTF-8"
	}
}

// Quality configuration is scoped to the currently configured Sub2 server.
// Never apply its numeric account/group IDs to a different saved connection.
func (s *Server) qualityTargetMatches(p model.FreeAccountProfile, cfg model.Sub2Settings) bool {
	if accountProvider(p) != "sub2" {
		return false
	}
	if p.DownstreamConnectionID == "" {
		return true
	}
	t, e := s.store.DownstreamTarget(p.DownstreamConnectionID)
	return e == nil && t.Provider == "sub2" && t.Sub2.URL == cfg.URL && t.Sub2.Email == cfg.Email
}

func (s *Server) rotateSavedDownstreamSecrets() error {
	sub, password, e := s.store.Sub2Settings()
	if e != nil {
		return e
	}
	if e = s.store.RotateDownstreamSecret("sub2", sub.URL, sub.Email, password); e != nil {
		return e
	}
	cpa, key, e := s.store.CPASettings()
	if e != nil {
		return e
	}
	if e = s.store.RotateDownstreamSecret("cpa", cpa.URL, "", key); e != nil {
		return e
	}
	vm, key, e := s.store.VM2APISettings()
	if e != nil {
		return e
	}
	return s.store.RotateDownstreamSecret("vm2api", vm.URL, "", key)
}
