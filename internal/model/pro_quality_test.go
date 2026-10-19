package model

import (
	"encoding/json"
	"testing"
)

func TestProQualityLegacySettingsDefaultFailureLimit(t *testing.T) {
	q := DefaultProQualitySettings()
	if err := json.Unmarshal([]byte(`{"enabled":true,"poll_interval_seconds":60,"action":"merge"}`), &q); err != nil {
		t.Fatal(err)
	}
	if q.ErrorFailureLimit != 2 || q.Validate() != nil {
		t.Fatalf("old settings lost default threshold: %+v", q)
	}
}
