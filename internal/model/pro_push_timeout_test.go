package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProPushTimeoutHistoricalPolicy(t *testing.T) {
	now := time.Now()
	c := DefaultProSettings()
	c.ForceMergeEnabled = false
	c.ForceMergeMinutes = 60
	p := MailAccountProfile{}
	p.CaptureProPushTimeout(c)
	c.ForceMergeEnabled = true
	p.CaptureProPushTimeout(c)
	p.ProPushStartedAt = &now
	if p.ProPushTimeoutEnabled == nil || *p.ProPushTimeoutEnabled || p.ProPushDeadline() != nil {
		t.Fatal("disabled opening retroactively timed")
	}
	p = MailAccountProfile{}
	p.CaptureProPushTimeout(c)
	if p.ProPushDeadline() != nil {
		t.Fatal("countdown began before success")
	}
	p.ProPushStartedAt = &now
	c.ForceMergeMinutes = 1
	c.ForceMergeEnabled = false
	p.CaptureProPushTimeout(c)
	if p.ProPushDeadline() == nil || !p.ProPushDeadline().Equal(now.Add(time.Hour)) {
		t.Fatal("global changes moved deadline")
	}
	b, _ := json.Marshal(p)
	var restored MailAccountProfile
	json.Unmarshal(b, &restored)
	if restored.ProPushDeadline() == nil || !restored.ProPushDeadline().Equal(*p.ProPushDeadline()) {
		t.Fatal("restart lost policy")
	}
	legacy := MailAccountProfile{ProPushStartedAt: &now}
	if legacy.ProPushDeadline() != nil {
		t.Fatal("unknown legacy treated as timed")
	}
}
