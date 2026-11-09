//go:build !windows

package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockSpool takes spool.lock with an exclusive, non-blocking flock. The lock
// lives as long as the returned file; closing it releases the lock. A lock
// held by another open file, in this or another process, returns
// ErrSpoolLocked.
func lockSpool(dir string) (*os.File, error) {
	path := filepath.Join(dir, LockFileName)
	f, errOpen := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if errOpen != nil {
		return nil, fmt.Errorf("archive: open %s: %w", path, errOpen)
	}
	if errLock := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); errLock != nil {
		_ = f.Close()
		if errors.Is(errLock, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrSpoolLocked, path)
		}
		return nil, fmt.Errorf("archive: flock %s: %w", path, errLock)
	}
	return f, nil
}
