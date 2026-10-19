package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
)

// Poll failures are counted once per completed query. For upstream detection
// failures, use the detection timestamp so polling one cached error cannot
// manufacture a streak. Persist both counters and sample identity across restarts.
func proQualityErrorObservation(state *model.ProQualityState, p model.MailAccountProfile, q model.ProQualitySettings, snapshot sub2.RemoteQualitySnapshot, v sub2.RemoteQualityResult, readErr error, count bool, now time.Time) bool {
	var sample string
	deadline := now.Add(time.Duration(q.MaxResultAgeSeconds) * time.Second)
	if readErr != nil && !errors.Is(readErr, context.Canceled) && !errors.Is(readErr, errProQualityClock) {
		sample = "query:" + now.Format(time.RFC3339Nano)
	} else if readErr == nil && state.Status == "error" && snapshot.Settings.Enabled &&
		snapshot.Settings.Revision != "" && v.Revision == snapshot.Settings.Revision && v.Version != "" && v.AccountID == p.Sub2AccountID {
		parts := []string{}
		for _, part := range []struct {
			key               string
			selected, enabled bool
			verdict           sub2.RemoteQualityVerdict
		}{{"question", q.QuestionEnabled, snapshot.Settings.QuestionEnabled, v.Question.RemoteQualityVerdict}, {"model", q.ModelAuditEnabled, snapshot.Settings.ModelAuditEnabled, v.Model.RemoteQualityVerdict}} {
			at := part.verdict.CheckedAt
			if !part.selected || !part.enabled || (part.verdict.Error == "" && part.verdict.Status != "error") || at == nil ||
				at.Before(proQualitySince(p)) || at.After(snapshot.ServerNow.Add(5*time.Second)) || snapshot.ServerNow.Sub(*at) > time.Duration(q.MaxResultAgeSeconds)*time.Second {
				continue
			}
			parts = append(parts, part.key+":"+at.Format(time.RFC3339Nano))
			until := now.Add(at.Add(time.Duration(q.MaxResultAgeSeconds) * time.Second).Sub(snapshot.ServerNow))
			if until.Before(deadline) {
				deadline = until
			}
		}
		if len(parts) > 0 {
			sample = v.Revision + ":" + strings.Join(parts, "|")
		}
	}
	if sample == "" {
		state.ErrorFailures, state.ErrorSample = 0, ""
		return false
	}
	if count {
		if sample != state.ErrorSample {
			state.ErrorFailures = min(state.ErrorFailures+1, q.ErrorFailureLimit)
		}
		state.ErrorSample = sample
	}
	if q.ErrorFailureLimit < 1 || state.ErrorFailures < q.ErrorFailureLimit {
		return false
	}
	state.ErrorDegraded, state.Degraded, state.SourceFresh = true, true, true
	state.Status, state.SourceRecovered, state.ValidUntil = "degraded", false, &deadline
	state.Reason = fmt.Sprintf("连续检测失败 %d 次，按降智处理", state.ErrorFailures)
	return true
}
