//go:build !unix

package launcher

import "os/exec"

func detachProxy(*exec.Cmd) {}
