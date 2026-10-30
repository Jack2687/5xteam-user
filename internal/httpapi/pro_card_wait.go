package httpapi

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Returned only before this attempt issues or pays. It keeps the same account
// queued; unknown financial operations continue to require reconciliation.
type proCardPreparationWait struct{ message string }

func (e *proCardPreparationWait) Error() string { return e.message }
func cardPreparationWait(format string, args ...any) error {
	return &proCardPreparationWait{message: fmt.Sprintf(format, args...)}
}

// Bound admission waits without weakening serialization or spawning a waiter
// which could later acquire an abandoned mutex.
func lockProCardPreparation(ctx context.Context, lock *sync.Mutex, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lock.TryLock() {
		return nil
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return cardPreparationWait("%s", message)
		case <-ticker.C:
			if lock.TryLock() {
				return nil
			}
		}
	}
}
