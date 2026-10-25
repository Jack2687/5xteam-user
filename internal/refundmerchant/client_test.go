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
