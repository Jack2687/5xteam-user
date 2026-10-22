package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"chatgpt-space-merge/internal/model"
)

func accountCredentialsTextLine(c model.MailAccountCredentials) (string, error) {
	fields := []string{c.Email, c.GptPassword, c.TotpSecret}
	for _, field := range fields {
		if strings.ContainsAny(field, "\r\n") || strings.Contains(field, "----") {
			return "", fmt.Errorf("字段包含换行或分隔符，无法按文本格式导出")
		}
	}
	return strings.Join(fields, "----"), nil
}

// Export saved credentials only. Missing fields stay empty to preserve all three
// columns; mail passwords, pickup URLs and current OTP codes are not substitutes.
func (s *Server) exportAccountCredentialsText(w http.ResponseWriter, r *http.Request, emails []string, pro bool) {
	var content strings.Builder
	seen := map[string]bool{}
	for index, email := range emails {
		if r.Context().Err() != nil {
			return
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if seen[email] {
			continue
		}
		p, c, err := s.store.MailAccountCredential(email)
		if err != nil || (p.ManagementScope == "pro") != pro {
			writeAPI(w, http.StatusConflict, nil, "账号不存在或已移动，请刷新后重新选择；未导出任何账号")
			return
		}
		c.Email = p.Email
		line, err := accountCredentialsTextLine(c)
		if err != nil {
			writeAPI(w, http.StatusConflict, nil, "账号 "+email+"："+err.Error())
			return
		}
		seen[email] = true
		content.WriteString(line + "\r\n")
		reportMailExportProgress(r.Context(), "processing", index+1, len(emails))
	}
	reportMailExportProgress(r.Context(), "generating", len(emails), len(emails))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="account-credentials-`+beijingNow().Format("20060102-150405")+`.txt"`)
	w.Header().Set("X-Export-Count", strconv.Itoa(len(seen)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content.String()))
}
