package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProQuotaDiscardsResponseAfterConnectionChange(t *testing.T) {
	for _, change := range []string{"server", "account"} {
		t.Run(change, func(t *testing.T) {
			f := newProExecutionFixture(t)
			f.s.proAutoCancel()
			f.s.proAutoWG.Wait()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
				case "/api/v1/admin/accounts/42/stats":
					fmt.Fprint(w, `{"code":0,"data":{"summary":{"total_standard_cost":1}}}`)
				case "/api/v1/admin/openai/accounts/42/quota":
					if change == "server" {
						v, _, _, _ := f.s.store.ProSettings()
						v.Sub2.URL = "https://new-downstream.invalid"
						if _, err := f.s.store.SaveProSettings(v, "", ""); err != nil {
							t.Error(err)
						}
					} else {
						if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.Sub2AccountID = 43 }); err != nil {
							t.Error(err)
						}
					}
					fmt.Fprint(w, `{"code":0,"data":{"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800}}}}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer up.Close()
			v, _, _, _ := f.s.store.ProSettings()
			v.Provider, v.Sub2.URL, v.Sub2.Email = "sub2", up.URL, "fixture@example.com"
			v.AutoMergeEnabled, v.QuotaUsedThreshold = true, 80
			if _, err := f.s.store.SaveProSettings(v, "fixture", ""); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				p.PushProvider = "sub2"
				p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 20, LimitWindowSeconds: 604800}
				p.QuotaCheckedAt = &old
			}); err != nil {
				t.Fatal(err)
			}
			_, err := f.s.performProQuota(t.Context(), f.email, true)
			p, _, readErr := f.s.store.MailAccountCredential(f.email)
			if err == nil || readErr != nil || p.Quota7D.UsedPercent != 20 || p.QuotaCheckedAt == nil || !p.QuotaCheckedAt.Equal(old) || p.ProWorkflowRunning || p.SpaceMergedOnce {
				t.Fatalf("stale quota applied to changed binding: error=%v quota=%+v", err, p.Quota7D)
			}
		})
	}
}
