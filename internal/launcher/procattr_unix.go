//go:build unix

package launcher

import (
	"os/exec"
	"syscall"
)

// detachProxy puts the started proxy in its own process group so that a
// terminal Ctrl-C reaches only the client; Run stops the proxy itself.
func detachProxy(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
