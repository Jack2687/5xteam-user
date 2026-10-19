package store

import (
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestProRestockExitEmergencyReservationAndEvidence(t *testing.T) {
	s := transferStore(t)
	transferSource(t, s, "exit-one@example.com")
	transferSource(t, s, "exit-two@example.com")
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID, c.ExitEnabled = true, "auto", 1, true
	c.TimeEnabled, c.ConcurrencyEnabled = false, false
	c.MinBatch, c.MaxBatch = 1, 1
	c, err := s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	snap := restock.Snapshot{ID: "exit-snapshot", At: now, Valid: true, LoadValid: true, GroupID: 1, Binding: "pool", ExitConfirmations: 2, Accounts: []restock.Account{
		{ID: 1, Ready: true, Capacity: 50, Used: 30, Windows: []restock.Window{{Seconds: 604800, ResetAt: now.Add(time.Hour).Unix(), At: now, Used: 96, Rate: 1, Samples: 2}}},
		{ID: 2, Ready: true, Capacity: 50, Used: 30},
	}}
	d := restock.Evaluate(c, snap, nil, restock.Limits{}, 0, 50, 2, now)
	d.ID = "exit-decision"
	// The previous batch was 30 s ago: only emergency cooldown has elapsed.
	if _, err = s.db.Exec(`INSERT INTO pro_restock_clock(id,last_batch) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET last_batch=excluded.last_batch`, formatTime(now.Add(-30*time.Second))); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ReserveProRestock(d, "target", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("emergency transaction rejected: %v %+v", err, tasks)
	}
	if tasks[0].Capacity != 50 {
		t.Fatal("task lost pinned capacity", tasks[0])
	}
	records, err := s.ProRestockDecisions(10, 0)
	if err != nil || len(records) != 1 || records[0].ExitNeed != 2 || records[0].Exit.AfterCapacity != 30 || records[0].Config.CapacityPercent != 60 || records[0].Exit.Demand != 60 {
		t.Fatalf("missing exit decision evidence: %+v %v", records, err)
	}
	if again, err := s.ReserveProRestock(d, "target", 0); len(again) > 0 || err == nil {
		t.Fatal("duplicate decision reserved again", again, err)
	}
}
