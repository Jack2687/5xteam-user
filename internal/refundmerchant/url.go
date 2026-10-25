package refundmerchant

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// Keep the complete submission endpoint exactly as configured. Custom paths
// are valid; no API version or resource name is appended or replaced.
func NormalizeSubmitURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultSubmitURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.Contains(raw, "\\") {
		return "", errors.New("API 商户地址无效，请填写完整的 HTTP 或 HTTPS 地址")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("API 商户地址须为 HTTP 或 HTTPS，不能包含账号密码、查询参数或页面锚点")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("API 商户地址不能包含相对路径")
		}
	}
	return u.String(), nil
}
