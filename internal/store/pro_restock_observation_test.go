package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/restock"
)

func TestRestockObservationCannotOverwriteDispatchOrStop(t *testing.T) {
	for _, scenario := range []string{"claimed", "completed", "stopped", "provider", "no_change"} {
		t.Run(scenario, func(t *testing.T) {
			s, claimed := preparationFixture(t)
			// Deterministically replay a monitor read before the dispatch boundary.
			reserved := claimed
			reserved.Status, reserved.SubmittedAt = "reserved", nil
			if err := s.SaveProRestockTask(reserved); err != nil {
				t.Fatal(err)
			}
			tasks, err := s.ProRestockTasks()
			if err != nil || len(tasks) != 1 {
				t.Fatal(err)
			}
			stale := tasks[0]
			if stale.ObservedPayload == "" {
				t.Fatal("missing observation version")
			}
			wire, _ := json.Marshal(stale)
			if strings.Contains(string(wire), "ObservedPayload") || strings.Contains(string(wire), "observed_payload") {
				t.Fatal("internal version exposed")
			}
			switch scenario {
			case "claimed":
				if err = s.ClaimProRestockDispatch(claimed); err != nil {
					t.Fatal(err)
				}
			case "completed":
				claimed.Status = "ready"
				claimed.CardID, claimed.OrderID = "original-card", "original-order"
				if err = s.SaveProRestockTask(claimed); err != nil {
					t.Fatal(err)
				}
			case "stopped":
				if _, _, err = s.StopProRestockTask(claimed.ID); err != nil {
					t.Fatal(err)
				}
			case "provider":
				if _, err = s.db.Exec(`UPDATE pro_restock_tasks SET payload=json_set(payload,'$.card_provider','ejh') WHERE id=?`, claimed.ID); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.ProRestockTask(claimed.ID)
			stale.Message = "本批排队，等待全自动执行名额"
			stale.UpdatedAt = time.Now()
			if err = s.SaveProRestockObservation(&stale); err != nil {
				t.Fatal(err)
			}
			got, _ := s.ProRestockTask(claimed.ID)
			if scenario != "no_change" {
				if mustJSON(got) != mustJSON(before) || mustJSON(stale) != mustJSON(before) {
					t.Fatalf("stale observation overwrote current lifecycle: %+v", got)
				}
			} else if got.Message != stale.Message {
				t.Fatal("fresh observation was not saved")
			}
			// Further observations use the reread version, without reviving a stop.
			if scenario != "stopped" {
				stale.Message = "next observation"
				if err = s.SaveProRestockObservation(&stale); err != nil {
					t.Fatal(err)
				}
				again, _ := s.ProRestockTask(claimed.ID)
				if again.Message != stale.Message {
					t.Fatal("new version not retained")
				}
			}
		})
	}
}

func TestRestockObservationRequiresReadVersion(t *testing.T) {
	s, task := preparationFixture(t)
	if err := s.SaveProRestockObservation(&restock.Task{ID: task.ID, Status: "reserved"}); err == nil {
		t.Fatal("blind observation was accepted")
	}
}
