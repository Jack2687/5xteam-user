package store

import (
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccManualAdmissionPreservesGuards(t *testing.T) {
	for _, scenario := range []string{"allowed", "default", "wrong", "disabled", "budget", "count", "unclaimed_task"} {
		t.Run(scenario, func(t *testing.T) {
			s := transferStore(t)
			cfg := model.DefaultProSettings()
			cfg.CardProvider, cfg.NexovccAutoOpen = "ejh", true
			cfg.NexovccDailyOpenLimit, cfg.NexovccDailyAmountLimit = 1, "10.00"
			if scenario == "disabled" {
				cfg.NexovccAutoOpen = false
			}
			if scenario == "budget" {
				cfg.NexovccDailyAmountLimit = "9.99"
			}
			if _, err := s.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			choice := NexovccOpenAdmission{ManualProvider: "nexovcc"}
			switch scenario {
			case "default":
				choice.ManualProvider = ""
			case "wrong":
				choice.ManualProvider = "ejh"
			case "unclaimed_task":
				choice.RestockID = "missing-task"
			}
			op := nexovcc.Operation{ID: "nex-manual-budget", Action: "open", Automatic: true, Amount: "10.00", Status: "submitted", CreatedAt: time.Now()}
			if scenario == "count" {
				previous := op
				previous.ID, previous.Status = "nex-previous", "succeeded"
				if err := s.BeginNexovccOperation(previous, choice); err != nil {
					t.Fatal(err)
				}
			}
			check := s.CheckNexovccOpen(op, choice)
			admit := s.BeginNexovccOperation(op, choice)
			if (check == nil) != (scenario == "allowed") || (admit == nil) != (scenario == "allowed") {
				t.Fatal("unexpected admission", check, admit)
			}
			if (scenario == "budget" || scenario == "count") && !strings.Contains(admit.Error(), "预算") {
				t.Fatal(admit)
			}
		})
	}
}
