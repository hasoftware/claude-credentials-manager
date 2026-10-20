//go:build !windows

package share

import (
	"os"
	"syscall"
	"testing"
)

// peerLock takes the exclusive lock store.WithCredentialLock uses
// (flock(2)), from an fd separate from the one ccm opens. flock is per
// open-file-description on Linux and macOS, so the two serialize even
// within one process.
func peerLock(t *testing.T, f *os.File) {
	t.Helper()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("peer flock LOCK_EX: %v", err)
	}
}

func peerUnlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
