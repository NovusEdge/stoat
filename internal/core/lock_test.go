package core

import (
	"errors"
	"sync"
	"testing"
)

// TestWithProvisionLockSerializes holds the lock from inside fn, then
// checks a concurrent withProvisionLock on the same dir gets
// ErrProvisionInProgress instead of running its own fn.
func TestWithProvisionLockSerializes(t *testing.T) {
	dir := t.TempDir()

	holding := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = WithProvisionLock(dir, func() error {
			close(holding)
			<-release
			return nil
		})
	}()

	<-holding
	var secondRan bool
	err := WithProvisionLock(dir, func() error {
		secondRan = true
		return nil
	})
	close(release)
	wg.Wait()

	if !errors.Is(err, ErrProvisionInProgress) {
		t.Fatalf("second withProvisionLock: got %v, want ErrProvisionInProgress", err)
	}
	if secondRan {
		t.Fatal("second withProvisionLock ran fn while the first held the lock")
	}
}

// TestWithProvisionLockReacquiresAfterRelease checks the lock is free again
// once the first holder's fn returns.
func TestWithProvisionLockReacquiresAfterRelease(t *testing.T) {
	dir := t.TempDir()

	if err := WithProvisionLock(dir, func() error { return nil }); err != nil {
		t.Fatalf("first withProvisionLock: %v", err)
	}

	var secondRan bool
	if err := WithProvisionLock(dir, func() error { secondRan = true; return nil }); err != nil {
		t.Fatalf("second withProvisionLock: %v", err)
	}
	if !secondRan {
		t.Fatal("second withProvisionLock did not run fn after the first released the lock")
	}
}
