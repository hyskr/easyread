package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// RetryWrite retries Windows filter-driver and sharing failures a bounded number
// of times. It never treats an existing lock as a transient write failure.
func RetryWrite(operation func() error) error {
	return retryWrite(operation, runtime.GOOS, time.Sleep)
}
func retryWrite(operation func() error, platform string, sleep func(time.Duration)) error {
	for attempt := 0; attempt < 5; attempt++ {
		err := operation()
		if err == nil {
			return nil
		}
		transient := platform == "windows" && (errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33)))
		if !transient || attempt == 4 {
			return err
		}
		sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	return nil
}

type lockIO struct {
	platform string
	sleep    func(time.Duration)
	write    func(*os.File, []byte) (int, error)
	close    func(*os.File) error
}

func nativeLockIO() lockIO {
	return lockIO{platform: runtime.GOOS, sleep: time.Sleep, write: func(f *os.File, b []byte) (int, error) { return f.Write(b) }, close: func(f *os.File) error { return f.Close() }}
}

// Lock shares the Python implementation's O_EXCL lock protocol across processes.
func Lock(root string) (func(), error) { return lockWithIO(root, 15*time.Second, nativeLockIO()) }

// LockTimeout is equivalent to Lock with an explicit acquisition timeout.
func LockTimeout(root string, timeout time.Duration) (func(), error) {
	return lockWithIO(root, timeout, nativeLockIO())
}
func lockWithIO(root string, timeout time.Duration, ops lockIO) (func(), error) {
	path := filepath.Join(root, ".write.lock")
	start := time.Now()
	retry := func(fn func() error) error { return retryWrite(fn, ops.platform, ops.sleep) }
	// Check identity on every removal attempt. A delayed unlock must not remove
	// another process's lock if a stale lock has been replaced in the meantime.
	removeOwned := func(owner os.FileInfo) error {
		return retry(func() error {
			current, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !os.SameFile(owner, current) {
				return nil
			}
			return os.Remove(path)
		})
	}
	for {
		var f *os.File
		err := retry(func() error {
			var err error
			f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			return err
		})
		if err == nil {
			owner, statErr := f.Stat()
			if statErr != nil {
				_ = ops.close(f)
				return nil, statErr
			}
			payload := []byte(fmt.Sprint(os.Getpid()))
			err = retry(func() error {
				_, err := f.Seek(0, 0)
				if err != nil {
					return err
				}
				n, err := ops.write(f, payload)
				if err == nil && n != len(payload) {
					return fmt.Errorf("short write of lock owner")
				}
				return err
			})
			closeErr := ops.close(f)
			if err != nil || closeErr != nil {
				_ = removeOwned(owner)
				if err != nil {
					return nil, err
				}
				return nil, closeErr
			}
			return func() { _ = removeOwned(owner) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, statErr := os.Lstat(path); statErr == nil && time.Since(st.ModTime()) > 30*time.Second {
			if err = removeOwned(st); err != nil {
				return nil, err
			}
			continue
		}
		if time.Since(start) >= timeout {
			return nil, fmt.Errorf("timed out waiting for lock: %s", path)
		}
		ops.sleep(50 * time.Millisecond)
	}
}
