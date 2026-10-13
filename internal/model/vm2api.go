package model

// VM2APISettings is a template for new Team cycles. Credentials live encrypted in the store.
type VM2APISettings struct {
	URL                            string   `json:"url"`
	KeyPresent                     bool     `json:"key_present"`
	PoolID                         string   `json:"pool_id"`
	BindProxy                      bool     `json:"bind_proxy"`
	ProxyIDs                       []string `json:"proxy_ids"`
	Kernel                         string   `json:"kernel"`
	RuntimeType                    string   `json:"runtime_type"`
	Memory                         string   `json:"memory"`
	VCPUs                          int      `json:"vcpus"`
	DiskGB                         int      `json:"disk_gb"`
	Region                         string   `json:"region"`
	Locale                         string   `json:"locale"`
	Enable401Check                 bool     `json:"enable_401_check"`
	StatusCheckIntervalSeconds     int      `json:"status_check_interval_seconds"`
	ReloginFailureLimit            int      `json:"relogin_failure_limit"`
	QuotaEnabled                   bool     `json:"quota_enabled"`
	QuotaCheckIntervalSeconds      int      `json:"quota_check_interval_seconds"`
	QuotaRemainingThresholdPercent float64  `json:"quota_remaining_threshold_percent"`
}

func DefaultVM2APISettings() VM2APISettings {
	return VM2APISettings{RuntimeType: "docker", Kernel: "ubuntu-24.04", Memory: "1g", VCPUs: 2, DiskGB: 20, Locale: "en_US.UTF-8", Enable401Check: true, StatusCheckIntervalSeconds: 120, ReloginFailureLimit: 2, QuotaEnabled: true, QuotaCheckIntervalSeconds: 120}
}

// DownstreamTarget is private storage, never an API response. A cycle pins its
// connection and policy before the first network side effect.
type DownstreamTarget struct {
	Scope    string         `json:"scope,omitempty"`
	Provider string         `json:"provider"`
	Sub2     Sub2Settings   `json:"sub2"`
	CPA      CPASettings    `json:"cpa"`
	VM2API   VM2APISettings `json:"vm2api"`
	Secret   string         `json:"secret"`
}
