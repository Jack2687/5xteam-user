package store

import (
	"chatgpt-space-merge/internal/model"
	"testing"
)

func TestVMQualityDefaultsAndCycleReset(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q, err := st.QualitySettings("vm2api")
	if err != nil || q.Action != "observe" || q.Enabled {
		t.Fatalf("unexpected defaults: %+v %v", q, err)
	}
	observed := model.AccountQuality{SourceProvider: "vm2api", Status: "degraded", Degraded: true, QuestionDegraded: true}
	if next := nextCycleQuality(observed); next.Degraded || next.Excluded || next.Status != "" {
		t.Fatal("observation leaked into next cycle")
	}
	observed.Excluded = true
	if !nextCycleQuality(observed).Excluded {
		t.Fatal("kicked account became reusable")
	}
}
