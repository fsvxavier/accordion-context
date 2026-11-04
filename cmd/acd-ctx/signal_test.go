package main

import "os"

// interrupt sends SIGINT to this process, which serve handles through
// signal.NotifyContext as it does for Ctrl-C.
func interrupt() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return p.Signal(os.Interrupt)
}
