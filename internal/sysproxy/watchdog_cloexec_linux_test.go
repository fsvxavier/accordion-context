//go:build linux

package sysproxy

import (
	"os"
	"syscall"
	"testing"
)

// The owner's write end of the watchdog pipe must be close-on-exec, so no
// process the owner starts (the client, nft) keeps the pipe open after the
// owner dies; otherwise the helper would never see EOF.
func TestWatchdogPipeWriteEndIsCloseOnExec(t *testing.T) {
	var gotW *os.File
	e := Env{Spawn: func(_ string, _ int, pipe *os.File) (func() error, error) {
		_ = pipe.Close()
		return func() error { return nil }, nil
	}}
	d, err := startWatchdog(e, "/x", 1)
	if err != nil {
		t.Fatal(err)
	}
	gotW = d.w
	defer gotW.Close()
	sc, err := gotW.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags uintptr
	var ferr syscall.Errno
	if err := sc.Control(func(fd uintptr) {
		flags, _, ferr = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if ferr != 0 || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("write end is not close-on-exec (flags %d, err %v)", flags, ferr)
	}
}
