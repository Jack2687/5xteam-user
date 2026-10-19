package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestSeatRecoveryExitRechecksAT(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, tc := range []struct {
			name            string
			status          int
			token           string
			wantLogin, fail bool
		}{
			{"valid", 200, recoveryAT("one", false), false, false},
			{"401", 401, recoveryAT("one", false), true, false},
			{"expired", 200, recoveryAT("one", true), true, false},
			{"missing", 200, "", true, false},
			{"malformed", 200, "bad", true, false},
			{"403", 403, recoveryAT("one", false), false, true},
			{"429", 429, recoveryAT("one", false), false, true},
			{"500", 500, recoveryAT("one", false), false, true},
			{"wrong identity", 200, recoveryAT("other", false), false, true},
		} {
			name := tc.name
			if manual {
				name = "manual/" + name
			} else {
				name = "automatic/" + name
			}
			t.Run(name, func(t *testing.T) {
				f := newRecoveryFixture(t)
				p := f.task(t, "one", "mother_invite", "child_leave")
				if manual {
					f.runUntil(t, p.ID, "dwell")
					f.allowDirectRemove = true
					recoveryControl(t, f, p.ID, "child_leave")
				} else {
					f.runUntil(t, p.ID, "leave")
				}
				if err := f.s.store.SaveSeatRecoveryLogin(p.ID, tc.token, "original-session"); err != nil {
					t.Fatal(err)
				}
				f.atStatus = tc.status
				before := len(f.mutations)
				logins := 0
				f.s.recovery.login = func(string, func(string), func(protocolOAuthDiagnostic)) (map[string]any, error) {
					logins++
					v, _ := f.s.teamRemoveLocks.LoadOrStore(p.TeamID, &sync.Mutex{})
					lock := v.(*sync.Mutex)
					if !lock.TryLock() {
						t.Error("login held mother lock")
					} else {
						lock.Unlock()
					}
					return map[string]any{"success": true, "access_token": "child-at-one", "session_token": "new-session"}, nil
				}
				f.s.runSeatRecoveryStep(t.Context(), p.ID)
				got := f.get(t, p.ID)
				if (logins > 0) != tc.wantLogin || (got.Status == "failed") != tc.fail {
					t.Fatalf("login=%d failed=%v message=%s", logins, got.Status == "failed", got.Message)
				}
				want := before + 1
				if tc.fail {
					want = before
				}
				if len(f.mutations) != want {
					t.Fatalf("mutations=%d want=%d", len(f.mutations), want)
				}
				_, creds, err := f.s.store.MailAccountCredential(p.Email)
				if err != nil {
					t.Fatal(err)
				}
				if creds.RefreshToken != "preserved-rt" {
					t.Fatal("RT overwritten")
				}
				if tc.wantLogin && creds.AccessToken != "child-at-one" {
					t.Fatal("new AT not saved")
				}
				if !tc.wantLogin && creds.ChatGPTSession != "original-session" {
					t.Fatal("original session overwritten")
				}
			})
		}
	}
}

func TestSeatRecoveryExitLoginFailureAndMembershipRecheck(t *testing.T) {
	for _, mode := range []string{"failure", "gone", "seat_changed", "deleted", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryFixture(t)
			p := f.task(t, "one", "mother_invite", "child_leave")
			f.runUntil(t, p.ID, "leave")
			before := len(f.mutations)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.s.recovery.login = func(string, func(string), func(protocolOAuthDiagnostic)) (map[string]any, error) {
				switch mode {
				case "failure":
					return nil, errors.New("fixture login failed")
				case "gone":
					f.mu.Lock()
					f.state["one"] = "outside"
					f.mu.Unlock()
				case "seat_changed":
					f.mu.Lock()
					f.state["one"] = "prolite"
					f.mu.Unlock()
				case "deleted":
					_, err := f.s.store.UpdateSeatRecoveryTask(p.ID, func(v *model.SeatRecoveryTask) { v.Finished = true })
					if err != nil {
						t.Fatal(err)
					}
				case "canceled":
					cancel()
				}
				return map[string]any{"success": true, "access_token": "child-at-one"}, nil
			}
			f.s.runSeatRecoveryStep(ctx, p.ID)
			if len(f.mutations) != before {
				t.Fatal("removal sent after login failed or membership changed")
			}
		})
	}
}

func TestSeatRecoveryExitDoesNotRetryAmbiguousRequest(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual"}[manual], func(t *testing.T) {
			f := newRecoveryFixture(t)
			p := f.task(t, "one", "mother_invite", "child_leave")
			if manual {
				f.runUntil(t, p.ID, "dwell")
				f.allowDirectRemove = true
				recoveryControl(t, f, p.ID, "child_leave")
			} else {
				f.runUntil(t, p.ID, "leave")
			}
			f.failLeave = true
			f.s.runSeatRecoveryStep(t.Context(), p.ID)
			before := len(f.mutations)
			f.s.recovery.login = func(string, func(string), func(protocolOAuthDiagnostic)) (map[string]any, error) {
				t.Error("unexpected repeat login")
				return nil, errors.New("no")
			}
			f.atStatus = http.StatusUnauthorized
			f.s.runSeatRecoveryStep(t.Context(), p.ID)
			if len(f.mutations) != before {
				t.Fatal("ambiguous leave repeated")
			}
		})
	}
}
