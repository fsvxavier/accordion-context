package attach

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// runChild starts the client with argv (argv[0] is the command) and env, and
// returns its exit status: its own code, or 128+signal when a signal ended it.
// Signals reach the client through rl, and ctx cancellation kills it.
func runChild(ctx context.Context, o Options, rl *relay, argv []string, dir string, env []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr
	started, err := rl.start(cmd)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return 127, fmt.Errorf("attach: start %s: %w", argv[0], err)
		}
		return 1, fmt.Errorf("attach: start %s: %w", argv[0], err)
	}
	if !started {
		return 130, fmt.Errorf("attach: interrupted (%v) before the client started", rl.interrupted())
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-done:
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
