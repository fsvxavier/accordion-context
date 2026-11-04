//go:build linux

package sysproxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// ExecSpawner starts the helper as this binary with WatchdogArg, in its own
// session, so a Ctrl-C or a closed terminal of the owner does not reach it. The
// pipe becomes descriptor 3 of the helper.
func ExecSpawner(statePath string, parentPID int, pipe *os.File) (func() error, error) {
	defer pipe.Close()
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(statePath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, WatchdogArg, "--state", abs, "--parent-pid", strconv.Itoa(parentPID))
	cmd.ExtraFiles = []*os.File{pipe}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Dir = "/"
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Wait, nil
}
