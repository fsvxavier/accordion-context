package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// recorder logs every command and forwards it to inner.
type recorder struct {
	mu    sync.Mutex
	inner Runner
	calls []Command
}

func (r *recorder) Run(ctx context.Context, c Command) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
	return r.inner.Run(ctx, c)
}

// argvs returns the command lines as "name arg arg" strings, for exact assertions.
func (r *recorder) argvs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = strings.Join(append([]string{c.Name}, c.Args...), " ")
	}
	return out
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// memGSettings is a gsettings store: "get" and "set" act on a map.
type memGSettings struct {
	vals    map[string]string
	failSet string // a "schema key" whose set fails
}

func (m *memGSettings) Run(_ context.Context, c Command) (string, error) {
	if c.Name != "gsettings" || len(c.Args) < 3 {
		return "", fmt.Errorf("unexpected command %s", c)
	}
	k := c.Args[1] + " " + c.Args[2]
	switch c.Args[0] {
	case "get":
		v, ok := m.vals[k]
		if !ok {
			return "", &ExitError{Command: c, Code: 1, Output: "no such key"}
		}
		return v + "\n", nil
	case "set":
		if k == m.failSet {
			return "", &ExitError{Command: c, Code: 1, Output: "boom"}
		}
		m.vals[k] = c.Args[3]
		return "", nil
	}
	return "", errors.New("unexpected gsettings verb")
}

// gnomeStart is the desktop value before acd-ctx runs.
func gnomeStart() map[string]string {
	return map[string]string{
		"org.gnome.system.proxy mode":         "'none'",
		"org.gnome.system.proxy ignore-hosts": "['localhost']",
		"org.gnome.system.proxy.https host":   "''",
		"org.gnome.system.proxy.https port":   "0",
	}
}

func gnomeEnv(r Runner, dir string) Env {
	return Env{
		GOOS:    "linux",
		Getenv:  func(k string) string { return map[string]string{"XDG_CURRENT_DESKTOP": "ubuntu:GNOME"}[k] },
		Runner:  r,
		Alive:   func(int, uint64) bool { return true },
		Geteuid: func() int { return 1000 },
	}
}
