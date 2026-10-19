package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNexovccImportWaitRetainsOriginalCardAndStopsSafely(t *testing.T) {
	for _, mode := range []string{"ready", "timeout", "stop", "cancel", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			f.failDetail = true
			if mode == "unknown" {
				f.noSerial = true
			}
			op, err := s.openNexovccLocked(context.Background(), "nex-waittest1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			if mode == "ready" {
				f.failDetail = false
			}
			checks := 0
			got, err := s.waitNexovccImportLocked(ctx, op, 30*time.Millisecond, time.Millisecond, func() error {
				checks++
				if mode == "stop" {
					return errors.New("user stopped")
				}
				return nil
			})
			if f.opens.Load() != 1 || got.ID != op.ID || got.Serial != op.Serial {
				t.Fatal("replaced original card", got)
			}
			if mode == "ready" {
				card, _, e := s.store.GPTPayCard(got.CardID)
				if err != nil || got.Status != "succeeded" || e != nil || !card.Available {
					t.Fatal(got, err, e)
				}
			} else if mode == "unknown" {
				if err != nil || got.Status != "unknown" || checks != 0 {
					t.Fatal("ambiguous issuance guessed", got, err)
				}
			} else if err == nil || got.Status != "import_pending" {
				t.Fatal("unsafe wait completion", got, err)
			}
			if mode == "timeout" && !strings.Contains(err.Error(), "原卡已保留") {
				t.Fatal(err)
			}
		})
	}
}
