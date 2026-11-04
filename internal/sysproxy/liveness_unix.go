//go:build unix

package sysproxy

import (
	"errors"
	"syscall"
)

// signalAlive reports whether pid exists. EPERM means it exists under another
// user, so it counts as alive.
func signalAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
