//go:build unix

package sysproxy

import (
	"fmt"
	"os"
	"syscall"
)

// checkTrusted refuses a path that another user could have planted or changed:
// it must not be a symlink, must be owned by the effective uid and must not be
// writable by group or others. A state file is replayed as commands, so a file
// another user can write must never be read.
func checkTrusted(path string, wantDir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("sysproxy: %s is a symlink; refusing it", path)
	}
	if wantDir != fi.IsDir() || (!wantDir && !fi.Mode().IsRegular()) {
		return fmt.Errorf("sysproxy: %s is not the expected kind of file", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("sysproxy: %s is owned by uid %d, not by uid %d; refusing it", path, st.Uid, os.Geteuid())
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("sysproxy: %s is writable by other users (mode %o); refusing it", path, fi.Mode().Perm())
	}
	return nil
}
