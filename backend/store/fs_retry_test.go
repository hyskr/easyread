package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// Mirrors tests/library/test_fs_retry.py::test_transient_invalid_argument_recovers.
func TestRetryWriteTransientInvalidArgumentRecovers(t *testing.T) {
	attempts, result := 0, 0
	sleeps := []time.Duration{}
	err := retryWrite(func() error {
		attempts++
		if attempts == 1 {
			return &os.PathError{Op: "write", Path: "sync", Err: syscall.EINVAL}
		}
		result = 42
		return nil
	}, "windows", func(d time.Duration) { sleeps = append(sleeps, d) })
	if err != nil || result != 42 || attempts != 2 || !reflect.DeepEqual(sleeps, []time.Duration{50 * time.Millisecond}) {
		t.Fatal(result, attempts, sleeps, err)
	}
}

// Mirrors tests/library/test_fs_retry.py::test_persistent_error_is_reported_after_bounded_retries.
func TestRetryWritePersistentErrorIsBounded(t *testing.T) {
	attempts := 0
	sleeps := []time.Duration{}
	err := retryWrite(func() error { attempts++; return syscall.EINVAL }, "windows", func(d time.Duration) { sleeps = append(sleeps, d) })
	if !errors.Is(err, syscall.EINVAL) || attempts != 5 {
		t.Fatal(attempts, err)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond, 200 * time.Millisecond}) {
		t.Fatal(sleeps)
	}
}

// Mirrors tests/library/test_fs_retry.py::test_existing_lock_is_never_bypassed.
func TestExistingLockNeverBypassed(t *testing.T) {
	root := t.TempDir()
	unlock, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if release, err := LockTimeout(root, 0); err == nil {
		release()
		t.Fatal("entered a locked directory")
	}
	if _, err := os.Stat(filepath.Join(root, ".write.lock")); err != nil {
		t.Fatal("existing lock removed", err)
	}
	unlock()
	if _, err := os.Stat(filepath.Join(root, ".write.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned lock not released", err)
	}
}

// Mirrors tests/library/test_fs_retry.py::test_lock_write_failure_closes_descriptor_and_removes_only_owned_lock.
func TestLockWriteFailureClosesDescriptorAndRemovesOwnedLock(t *testing.T) {
	root := t.TempDir()
	ops := nativeLockIO()
	closes := 0
	var fd *os.File
	ops.write = func(f *os.File, _ []byte) (int, error) { fd = f; return 0, syscall.ENOSPC }
	ops.close = func(f *os.File) error { closes++; return f.Close() }
	if release, err := lockWithIO(root, 0, ops); !errors.Is(err, syscall.ENOSPC) {
		if release != nil {
			release()
		}
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatal("descriptor close count", closes)
	}
	if _, err := fd.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("descriptor remains open", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".write.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed owned lock retained", err)
	}
}
func TestRetryWriteOnlyRetriesWindowsTransientErrors(t *testing.T) {
	for _, tc := range []struct {
		platform string
		err      error
		want     int
	}{{"linux", syscall.EINVAL, 1}, {"windows", syscall.ENOSPC, 1}, {"windows", os.ErrExist, 1}, {"windows", syscall.Errno(32), 5}, {"windows", syscall.Errno(33), 5}} {
		attempts := 0
		err := retryWrite(func() error { attempts++; return tc.err }, tc.platform, func(time.Duration) {})
		if !errors.Is(err, tc.err) || attempts != tc.want {
			t.Fatal(tc, attempts, err)
		}
	}
}
func TestLockRetriesOwnerWriteWithoutBypassingOwnership(t *testing.T) {
	root := t.TempDir()
	ops := nativeLockIO()
	ops.platform = "windows"
	ops.sleep = func(time.Duration) {}
	attempts := 0
	ops.write = func(f *os.File, b []byte) (int, error) {
		attempts++
		if attempts < 3 {
			return 0, syscall.EINVAL
		}
		return f.Write(b)
	}
	unlock, err := lockWithIO(root, 0, ops)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if attempts != 3 {
		t.Fatal(attempts)
	}
	b, err := os.ReadFile(filepath.Join(root, ".write.lock"))
	if err != nil || len(b) == 0 {
		t.Fatal(string(b), err)
	}
}
func TestOldUnlockPreservesReplacementLock(t *testing.T) {
	root := t.TempDir()
	unlock, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".write.lock")
	old := filepath.Join(root, "previous.lock")
	if err = os.Rename(p, old); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte("another owner"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock()
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "another owner" {
		t.Fatal(string(b), err)
	}
}
