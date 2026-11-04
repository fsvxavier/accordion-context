//go:build !unix

package sysproxy

import (
	"fmt"
	"os"
)

// checkTrusted refuses symlinks. Ownership is not checked off Unix.
func checkTrusted(path string, wantDir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || wantDir != fi.IsDir() {
		return fmt.Errorf("sysproxy: %s is not the expected kind of file", path)
	}
	return nil
}
