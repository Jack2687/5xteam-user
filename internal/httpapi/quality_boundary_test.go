package httpapi

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
)

func TestRemoteQualityPushBoundary(t *testing.T) {
	since := time.Date(2026, 10, 17, 0, 45, 12, 24665973, time.FixedZone("CST", 8*60*60))
	now := since.Add(40 * time.Minute)
	for _, tc := range []struct {
		name   string
		change func(*sub2.RemoteQualitySnapshot, *sub2.RemoteQualityResult)
		bad    bool
		normal bool
	}{
		{name: "normal streak started 74ms before push acknowledgement", normal: true},
		{name: "degraded streak started 74ms before push acknowledgement", bad: true, change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			r.Question.Status, r.Question.Degraded = "degraded", true
			r.Question.Failures, r.Question.Successes = 2, 0
		}},
		{name: "exact tolerance boundary", normal: true, change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			at := since.Add(-5 * time.Second)
			r.Question.StreakStartedAt = &at
		}},
		{name: "older cycle streak rejected", change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			at := since.Add(-5*time.Second - time.Nanosecond)
			r.Question.StreakStartedAt = &at
		}},
		{name: "old evidence remains rejected even within tolerance", change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			at := since.Add(-time.Millisecond)
			r.Question.EvidenceAt = &at
		}},
		{name: "expired latest sample rejected", change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			at := now.Add(-16 * time.Minute)
			r.Question.EvidenceAt = &at
		}},
		{name: "missing streak rejected", change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			r.Question.StreakStartedAt = nil
		}},
		{name: "insufficient recovery samples rejected", change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			r.Question.Successes = 1
		}},
		{name: "disabled source rejected", change: func(s *sub2.RemoteQualitySnapshot, _ *sub2.RemoteQualityResult) {
			s.Settings.Enabled = false
		}},
		{name: "old settings revision rejected", change: func(_ *sub2.RemoteQualitySnapshot, r *sub2.RemoteQualityResult) {
			r.Revision = "old"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := model.DefaultQualitySettings()
			q.QuestionEnabled, q.ModelAuditEnabled = true, false
			s, r := qualityRemoteExample(model.FreeAccountProfile{})
			s.ServerNow = now
			streak, evidence := since.Add(-74246094*time.Nanosecond), now.Add(-time.Minute)
			r.Question.RemoteQualityVerdict = sub2.RemoteQualityVerdict{Status: "normal", Successes: 13, EvidenceAt: &evidence, CheckedAt: &evidence, StreakStartedAt: &streak}
			if tc.change != nil {
				tc.change(&s, &r)
			}
			bad, normal := remoteQualityDecisionSince(q, since, s, r)
			if bad != tc.bad || normal != tc.normal {
				t.Fatalf("bad=%t normal=%t, want bad=%t normal=%t", bad, normal, tc.bad, tc.normal)
			}
		})
	}
}

func TestProQualityNormalAfterPushAcknowledgementRace(t *testing.T) {
	now := time.Now().UTC()
	pushed := now.Add(-40 * time.Minute)
	p := model.MailAccountProfile{ID: "pro-boundary", CreatedAt: pushed.Add(-time.Hour), ProPushStartedAt: &pushed, Sub2AccountID: 3246}
	c := model.DefaultProSettings()
	q := model.DefaultProQualitySettings()
	s, r := qualityRemoteExample(model.FreeAccountProfile{})
	s.ServerNow = now
	streak, evidence := pushed.Add(-74246094*time.Nanosecond), now.Add(-time.Minute)
	r.Question.RemoteQualityVerdict = sub2.RemoteQualityVerdict{Status: "normal", Successes: 13, EvidenceAt: &evidence, CheckedAt: &evidence, StreakStartedAt: &streak}
	r.Model = sub2.RemoteQualityModel{}
	s.Settings.ModelAuditEnabled = false
	p.ProQuality.Status, p.ProQuality.Error = "unknown", "old pending message"
	state, bad, normal := proQualityObservation(p, q, c, s, r, nil)
	if bad || !normal || state.Status != "normal" || !state.SourceFresh || state.Error != "" || state.ValidUntil == nil || !state.ValidUntil.After(now) {
		t.Fatalf("normal results stayed pending: bad=%t normal=%t state=%+v", bad, normal, state)
	}
	// A later push must not reuse the previous binding's streak or latest sample.
	repushed := now.Add(-30 * time.Second)
	p.ProQualitySince = &repushed
	state, bad, normal = proQualityObservation(p, q, c, s, r, nil)
	if bad || normal || state.Status != "unknown" || state.SourceFresh || state.ValidUntil != nil {
		t.Fatalf("repush accepted old results: bad=%t normal=%t state=%+v", bad, normal, state)
	}
}
