package sub2

import (
	"testing"
	"time"
)

func TestMonitorReadySupportsSub2ModelPatterns(t *testing.T) {
	a := MonitorAccount{Platform: "openai", Status: "active", Schedulable: true, Concurrency: 10}
	for _, tc := range []struct {
		pattern, model string
		want           bool
	}{
		{"gpt-*", "gpt-5", true}, {"*", "gpt-5", true},
		{"gpt-5", " gpt-5 ", true}, {"gpt-*", "o3", false},
		{"gpt-?", "gpt-5", false}, {"gpt-*-x", "gpt-5-x", false},
	} {
		a.Credentials.Models = map[string]string{tc.pattern: "mapped"}
		if got := a.Ready(time.Now(), []string{tc.model}); got != tc.want {
			t.Errorf("%q %q: ready=%v want=%v", tc.pattern, tc.model, got, tc.want)
		}
	}
	a.Credentials.Models = map[string]string{"gpt-*": "mapped"}
	if a.Ready(time.Now(), []string{"gpt-5", "o3"}) {
		t.Fatal("must cover every requested model")
	}
}
