package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

func cancelFixtureRequest(s *Server, cardID, requestID, action string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"`+action+`","amount":"10","confirm":true,"request_id":"`+requestID+`"}`))
	r.SetPathValue("id", cardID)
	w := httptest.NewRecorder()
	s.ejhCardAction(w, r)
	return w
}

func TestEJHCancellationAsyncBusinessErrorRecoversWithoutResubmission(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	remote := ejh.Card{OrderNo: "async-card", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "3"}
	card, err := s.store.ImportEJHCard(remote, true)
	if err != nil {
		t.Fatal(err)
	}
	cancelCalls, queryCalls := 0, 0
	remote.Status, remote.Locked = "8", "1"
	queryFails := false
	s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, data map[string]any) any {
		if data["orderNo"] != remote.OrderNo {
			t.Fatal("queried another card")
		}
		switch method {
		case "card.cancel.info":
			cancelCalls++
			return &ejh.APIError{Code: "300002", Message: "fixture issuer processing"}
		case "card.query.info":
			queryCalls++
			if queryFails {
				return &ejh.APIError{Code: "300002"}
			}
			return remote
		default:
			t.Fatalf("unexpected mutation: %s", method)
			return nil
		}
	})
	const requestID = "async-cancellation"
	w := cancelFixtureRequest(s, card.ID, requestID, "cancel")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"processing"`) || strings.Contains(w.Body.String(), "300002") {
		t.Fatal("pending cancellation reported as error", w.Code, w.Body.String())
	}
	w = cancelFixtureRequest(s, card.ID, requestID, "cancel")
	if w.Code != 200 || cancelCalls != 1 || queryCalls != 1 {
		t.Fatal("replayed issuer mutation", w.Code, cancelCalls, queryCalls)
	}
	w = cancelFixtureRequest(s, card.ID, "another-request-id", "increase")
	if w.Code != 409 {
		t.Fatal("admitted funds operation during cancellation", w.Code)
	}
	local, _, err := s.store.GPTPayCard(card.ID)
	if err != nil || local.Available || local.IssuerCancelStatus != "processing" {
		t.Fatal("lost cancellation guard", local, err)
	}
	due := func() {
		t.Helper()
		var op ejh.Operation
		if err := s.store.EJHGet("operation", requestID, &op); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-time.Minute)
		op.NextCheckAt = &past
		if err := s.store.EJHPut("operation", op.ID, op); err != nil {
			t.Fatal(err)
		}
	}
	s.tickEJHCancellations(context.Background())
	if queryCalls != 1 {
		t.Fatal("ignored poll interval")
	}
	due()
	queryFails = true
	s.tickEJHCancellations(context.Background())
	var op ejh.Operation
	if err = s.store.EJHGet("operation", requestID, &op); err != nil || op.Status != "processing" || op.CheckError == "" {
		t.Fatal("query failure erased pending state", op, err)
	}
	// A later query confirms success; it does not submit another cancellation.
	due()
	queryFails = false
	remote.Status = "2"
	s.tickEJHCancellations(context.Background())
	op = ejh.Operation{}
	if err = s.store.EJHGet("operation", requestID, &op); err != nil || op.Status != "completed" || op.Error != "" || op.CheckError != "" {
		t.Fatal("completion not reconciled", op, err)
	}
	local, _, err = s.store.GPTPayCard(card.ID)
	if err != nil || local.IssuerStatus != "2" || local.IssuerCancelStatus != "" || local.Available {
		t.Fatal("closed card state incorrect", local, err)
	}
	s.tickEJHCancellations(context.Background())
	if cancelCalls != 1 || queryCalls != 3 {
		t.Fatal("terminal operation polled/replayed", cancelCalls, queryCalls)
	}
	// Dashboard must agree with the persisted action and hide card credentials.
	w = httptest.NewRecorder()
	s.ejhDashboard(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), remote.Number) || strings.Contains(w.Body.String(), `"securityCode":"123"`) {
		t.Fatal("dashboard failed/redaction", w.Code)
	}
}

func TestEJHCancellationRequiresMatchingIssuerEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, status, want string
		accepted, mismatch bool
	}{
		{"accepted_still_active", "3", "processing", true, false},
		{"generic_business_error", "3", "unknown", false, false},
		{"issuer_failed", "6", "failed", false, false},
		{"wrong_card", "8", "unknown", false, true},
		{"already_complete", "2", "completed", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := ejhTestServer(t, 200)
			remote := ejh.Card{OrderNo: "evidence-card", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "3"}
			card, err := s.store.ImportEJHCard(remote, true)
			if err != nil {
				t.Fatal(err)
			}
			s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, _ map[string]any) any {
				if method == "card.cancel.info" {
					if tc.accepted {
						return map[string]any{"orderNo": remote.OrderNo, "status": "SUCCESS"}
					}
					return &ejh.APIError{Code: "300002"}
				}
				if method != "card.query.info" {
					t.Fatal(method)
				}
				result := remote
				result.Status = tc.status
				if tc.mismatch {
					result.OrderNo = "unrelated-card"
				}
				return result
			})
			w := cancelFixtureRequest(s, card.ID, "evidence-cancellation", "cancel")
			var response struct {
				Data ejh.Operation `json:"data"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Data.Status != tc.want {
				t.Fatal(w.Code, w.Body.String(), err)
			}
			local, _, err := s.store.GPTPayCard(card.ID)
			if err != nil || local.Available {
				t.Fatal("unsafe card became available", err)
			}
			if tc.want == "unknown" || tc.want == "processing" {
				if local.IssuerCancelStatus != tc.want {
					t.Fatal("pending reason lost", local)
				}
			}
			if tc.mismatch && local.IssuerStatus != "3" {
				t.Fatal("wrong card changed snapshot")
			}
		})
	}
}
