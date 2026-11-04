//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package mitm

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockCADir takes an exclusive flock on dir/ca.lock. The returned function
// releases it. The lock is per open file, so it also serialises goroutines.
func lockCADir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, caLockName), os.O_CREATE|os.O_RDWR, keyMode)
	if err != nil {
		return nil, fmt.Errorf("mitm: open CA lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mitm: lock CA dir: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
