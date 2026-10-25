package refundmerchant

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMerchantProtocol(t *testing.T) {
	status := "queued"
	identity := "person@example.com"
	posts, gets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing key")
		}
		if r.Method == "POST" {
			posts++
			body, _ := io.ReadAll(r.Body)
			if r.URL.Path != "/v1/refunds" || r.Header.Get("Idempotency-Key") != "space-test-id" || string(body) != `{"authorized":true,"accounts":[{"email":"person@example.com","password":"secret","totp_secret":"JBSWY3DPEHPK3PXP"}]}` {
				t.Error("incorrect request")
			}
			w.WriteHeader(202)
			fmt.Fprint(w, `{"batch_id":"batch-1","jobs":[{"id":"job-1","email":"person@example.com","status":"queued"}]}`)
			return
		}
		gets++
		if r.URL.Path != "/v1/refunds/job-1" {
			t.Error("wrong progress job")
		}
		fmt.Fprintf(w, `{"id":"job-1","email":%q,"status":%q}`, identity, status)
	}))
	defer server.Close()
	c := Client{BaseURL: server.URL}
	receipt, err := c.Submit(t.Context(), "test-key", "space-test-id", []byte(`{"authorized":true,"accounts":[{"email":"person@example.com","password":"secret","totp_secret":"JBSWY3DPEHPK3PXP"}]}`), identity)
	id := receipt.JobID
	if err != nil || id != "job-1" || receipt.BatchID != "batch-1" {
		t.Fatal(receipt, err)
	}
	for _, state := range []string{"queued", "processing", "verifying", "attention", "succeeded", "failed", "cancelled", "unexpected"} {
		status = state
		job, err := c.Progress(t.Context(), "test-key", id, identity)
		if err != nil || job.Succeeded() != (state == "succeeded") {
			t.Fatalf("state %s: %v", state, err)
		}
	}
	identity = "another@example.com"
	if _, err = c.Progress(t.Context(), "test-key", id, "person@example.com"); err == nil {
		t.Fatal("accepted wrong account")
	}
	if posts != 1 || gets != 9 {
		t.Fatal(posts, gets)
	}
}

func TestMerchantRejectsUnprovenResponses(t *testing.T) {
	for _, raw := range []string{`{"items":[{"id":"x","email":"other@example.com"}]}`, `{"jobs":[{"id":"one"},{"id":"two"}]}`, `{"id":"../../other"}`, `{"success":true}`, `<html>accepted</html>`} {
		t.Run(raw, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202); fmt.Fprint(w, raw) }))
			defer srv.Close()
			if _, err := (Client{BaseURL: srv.URL}).Submit(t.Context(), "key", "space-test", nil, "person@example.com"); err == nil {
				t.Fatal("accepted ambiguous job")
			}
		})
	}
	for _, raw := range []string{`{"id":"job","email":"person@example.com"}`, `{"id":"other","email":"person@example.com","status":"succeeded"}`, `{"id":"job","status":"succeeded"}`, `{"status":"succeeded"}`, `null`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, raw) }))
		if _, err := (Client{BaseURL: srv.URL}).Progress(t.Context(), "key", "job", "person@example.com"); err == nil {
			t.Fatal("accepted unproven final result")
		}
		srv.Close()
	}
}

func TestMerchantNoRedirectOrSecretErrors(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(307)
		fmt.Fprint(w, "password-secret totp-secret bearer-key")
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	_, err := c.Submit(t.Context(), "bearer-key", "space-key", nil, "person@example.com")
	if err == nil || calls != 0 || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "bearer-key") {
		t.Fatal("redirected or leaked secrets", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = c.Progress(ctx, "key", "job", "person@example.com"); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestMerchantBatchReceiptRequiresAccountResolution(t *testing.T) {
	posts, lists, details := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			w.WriteHeader(202)
			fmt.Fprint(w, `{"batch_id":"batch","id":"batch"}`)
			return
		}
		if r.URL.Path == "/v1/refunds" {
			lists++
			if r.URL.Query().Get("batch_id") != "batch" || r.URL.Query().Get("page_size") != "100" {
				t.Error("missing batch filter")
			}
			fmt.Fprint(w, `{"items":[{"id":"actual-job","batch_id":"batch","email":"person@example.com","status":"succeeded"}]}`)
			return
		}
		details++
		if r.URL.Path != "/v1/refunds/actual-job" {
			t.Error("queried batch as job")
		}
		fmt.Fprint(w, `{"id":"actual-job","email":"person@example.com","status":"succeeded"}`)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	receipt, err := c.Submit(t.Context(), "key", "space-key", nil, "person@example.com")
	if err != nil || receipt.BatchID != "batch" || receipt.JobID != "" {
		t.Fatal(receipt, err)
	}
	id, err := c.ResolveBatch(t.Context(), "key", receipt.BatchID, "person@example.com")
	if err != nil || id != "actual-job" {
		t.Fatal(id, err)
	}
	job, err := c.Progress(t.Context(), "key", id, "person@example.com")
	if err != nil || !job.Succeeded() || posts != 1 || lists != 1 || details != 1 {
		t.Fatal(job, err)
	}
}

func TestMerchantCustomAddressAndWorkbenchEnvelope(t *testing.T) {
	for _, path := range []string{"/v1/refunds", "/v1/cemifh", "/gateway/v1/cemifh/", "/open/v1/requests"} {
		t.Run(path, func(t *testing.T) {
			posts, batches, details := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer custom-key" {
					t.Error("missing custom key")
				}
				switch r.Method + " " + r.URL.Path {
				case "POST " + path:
					posts++
					if r.Header.Get("Idempotency-Key") != "unique-key" {
						t.Error("missing idempotency key")
					}
					w.WriteHeader(202)
					fmt.Fprint(w, `{"ids":["workbench-job"],"status":"queued"}`)
				case "GET " + path:
					batches++
					if r.URL.Query().Get("batch_id") != "custom-batch" || r.URL.Query().Get("page_size") != "100" {
						t.Error("missing batch filter")
					}
					fmt.Fprint(w, `{"items":[{"id":"workbench-job","batch_id":"custom-batch","email":"person@example.com"}]}`)
				case "GET " + strings.TrimRight(path, "/") + "/workbench-job":
					details++
					fmt.Fprint(w, `{"refund":{"id":"workbench-job","email":"person@example.com","status":"succeeded"},"events":[]}`)
				default:
					t.Error("wrong custom path", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			c := Client{SubmitURL: srv.URL + path}
			receipt, err := c.Submit(t.Context(), "custom-key", "unique-key", nil, "person@example.com")
			if err != nil || receipt.JobID != "workbench-job" {
				t.Fatal(receipt, err)
			}
			job, err := c.Progress(t.Context(), "custom-key", receipt.JobID, "person@example.com")
			if err != nil || !job.Succeeded() {
				t.Fatal(job, err)
			}
			if _, err = c.Progress(t.Context(), "custom-key", receipt.JobID, "different@example.com"); err == nil {
				t.Fatal("envelope bypassed account identity")
			}
			id, err := c.ResolveBatch(t.Context(), "custom-key", "custom-batch", "person@example.com")
			if err != nil || id != receipt.JobID || posts != 1 || batches != 1 || details != 2 {
				t.Fatal("custom endpoint submit/progress/batch mismatch", id, err, posts, batches, details)
			}
		})
	}
}

func TestNormalizeMerchantAPIURL(t *testing.T) {
	for input, want := range map[string]string{
		"":                        DefaultSubmitURL,
		" https://example.com/ ":  "https://example.com/",
		"https://example.com/v1/": "https://example.com/v1/",
		"https://example.com/service/v1/refunds/": "https://example.com/service/v1/refunds/",
		"http://127.0.0.1:18740/v1/refunds":       "http://127.0.0.1:18740/v1/refunds",
		"http://127.0.0.1:18740/v1/cemifh":        "http://127.0.0.1:18740/v1/cemifh",
		"http://127.0.0.1:18740":                  "http://127.0.0.1:18740",
	} {
		got, err := NormalizeSubmitURL(input)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"example.com", "ftp://example.com", "https:///v1", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com/#refunds", "https://example.com/a/../b", "https://example.com/a/%2e%2e/b", "http://exa mple.com", "http://example.com\\path"} {
		if _, err := NormalizeSubmitURL(input); err == nil {
			t.Errorf("invalid address accepted: %q", input)
		}
	}
}
