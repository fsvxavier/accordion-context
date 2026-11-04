package attach

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// relay owns the interrupt signals of one run. Until the client starts, a
// signal cancels the integration checks, so the run ends and the deferred
// cleanup removes its temp files. After the client starts, a signal is
// forwarded to it. The relay is subscribed before any file is created.
type relay struct {
	sigs     <-chan os.Signal
	notified chan os.Signal // set when the relay subscribed to the process
	cancel   context.CancelFunc
	stop     chan struct{}
	done     chan struct{}

	mu   sync.Mutex
	proc *os.Process
	got  os.Signal
}

// newRelay starts the relay. A nil in subscribes to SIGINT, SIGTERM and
// SIGHUP; a non-nil in is used instead, which lets tests inject signals.
func newRelay(in <-chan os.Signal, cancel context.CancelFunc) *relay {
	r := &relay{sigs: in, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	if in == nil {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		r.notified = ch
		r.sigs = ch
	}
	go r.loop()
	return r
}

func (r *relay) loop() {
	defer close(r.done)
	for {
		select {
		case s := <-r.sigs:
			r.deliver(s)
		case <-r.stop:
			return
		}
	}
}

func (r *relay) deliver(s os.Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil {
		_ = r.proc.Signal(s)
		return
	}
	if r.got == nil {
		r.got = s
	}
	r.cancel()
}

// interrupted returns the first signal received before the client started,
// or nil.
func (r *relay) interrupted() os.Signal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got
}

// start runs cmd unless an interrupt arrived first. The lock is held across
// Start, so a signal either cancels the start or reaches the new process.
func (r *relay) start(cmd *exec.Cmd) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.got != nil {
		return false, nil
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	r.proc = cmd.Process
	return true, nil
}

// close stops the relay and releases the process subscription.
func (r *relay) close() {
	close(r.stop)
	<-r.done
	if r.notified != nil {
		signal.Stop(r.notified)
	}
}
