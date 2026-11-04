//go:build unix

package launcher

import (
	"context"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunForwardsSignalToClient sends SIGTERM to the launcher process; Run
// must forward it to the client, which traps it and exits with 42.
func TestRunForwardsSignalToClient(t *testing.T) {
	addr := healthServer(t, "v1")
	prof := fakeProfile()
	out := &syncBuffer{}
	type result struct {
		code int
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		code, err := Run(context.Background(), Options{
			Args:      shellArgs(`trap 'exit 42' TERM; echo ready; while :; do sleep 0.05; done`),
			Profile:   &prof,
			Cwd:       t.TempDir(),
			Version:   "v1",
			ProxyAddr: addr,
			Stdin:     strings.NewReader(""),
			Stdout:    out,
			Stderr:    io.Discard,
		})
		resc <- result{code, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "ready") {
		if time.Now().After(deadline) {
			t.Fatal("client never reported ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-resc:
		if r.err != nil || r.code != 42 {
			t.Fatalf("Run = (%d, %v), want (42, nil)", r.code, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after forwarded signal")
	}
}
