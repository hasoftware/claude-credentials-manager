//go:build windows

package share

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// peerLock takes the exclusive lock store.WithCredentialLock uses
// (LockFileEx on byte 0), from a handle separate from the one ccm
// opens. LockFileEx locks belong to the handle, so the two serialize
// even within one process.
func peerLock(t *testing.T, f *os.File) {
	t.Helper()
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
	if err != nil {
		t.Fatalf("peer LockFileEx: %v", err)
	}
}

func peerUnlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
