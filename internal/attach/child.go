package attach

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// runChild starts the client with env and returns its exit status: its own
// code, or 128+signal when a signal ended it. Signals received here are
// forwarded to the client, and ctx cancellation kills it.
func runChild(ctx context.Context, o Options, dir string, env []string) (int, error) {
	cmd := exec.Command(o.Args[0], o.Args[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return 127, fmt.Errorf("attach: start %s: %w", o.Args[0], err)
		}
		return 1, fmt.Errorf("attach: start %s: %w", o.Args[0], err)
	}
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigc)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigc:
				_ = cmd.Process.Signal(s)
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return
			case <-done:
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	return exitStatus(waitErr), nil
}

// exitStatus maps a Wait error to a shell-style exit code.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
