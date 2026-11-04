package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// restoreTimeout bounds a restore. Callers pass a context that is not canceled
// by the signal that started the restore, so use RestoreContext.
const restoreTimeout = 30 * time.Second

// RestoreContext returns the context for restores: it is independent of the
// run's context, which an interrupt may already have canceled.
func RestoreContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), restoreTimeout)
}

// Active is a machine change that is in effect until Stop.
type Active struct {
	env   Env
	mode  string
	state *State
	path  string
	back  Backend
	plan  NFTPlan
	fw    string
	dog   *watchdog
	done  bool
}

// newTransparentState describes this run's transparent change, before the first
// change is made.
func newTransparentState(e Env, fw string) *State {
	pid := os.Getpid()
	return &State{
		Mode: ModeTransparent, Firewall: fw, PID: pid,
		StartTime: startTime(e.procRoot(), pid), Created: e.now(),
	}
}

// beginTransparent saves the state file, then starts the watchdog that owns it.
// Both come before the first firewall change.
func beginTransparent(e Env, dir string, st *State, p NFTPlan, fw string) (*Active, error) {
	path, err := createState(dir, st)
	if err != nil {
		return nil, err
	}
	a := &Active{env: e, mode: ModeTransparent, state: st, path: path, plan: p, fw: fw}
	// The helper refuses a state file it does not trust, and the owner would not
	// learn of it. Run the same checks here, so a helper that would exit at once
	// never lets the redirect be installed unprotected.
	if _, err := readState(path); err != nil {
		return nil, a.unapplied(err)
	}
	if err := checkWatchdogState(st, st.PID); err != nil {
		return nil, a.unapplied(err)
	}
	dog, err := startWatchdog(e, path, st.PID)
	if err != nil {
		return nil, a.unapplied(err)
	}
	a.dog = dog
	return a, nil
}

// unapplied ends a transparent start whose change was not made (or was refused
// atomically): the helper stands down and the state file goes.
func (a *Active) unapplied(cause error) error {
	errs := []error{cause}
	if err := a.dog.standDown(); err != nil {
		errs = append(errs, err)
	}
	if err := removeFile(a.path); err != nil {
		errs = append(errs, err)
	}
	a.done = true
	return errors.Join(errs...)
}

// StartSystemProxy points the system HTTPS proxy at ep. It saves the previous
// values first. If any write or the verification fails, it rolls back and
// returns the error.
func StartSystemProxy(ctx context.Context, e Env, dir string, ep Endpoint) (*Active, error) {
	r, err := e.runner()
	if err != nil {
		return nil, err
	}
	// A leftover state file means settings may still hold a crashed run's
	// values. Snapshotting them now would make this run restore them later.
	if paths, err := listStates(dir); err != nil {
		return nil, err
	} else if len(paths) > 0 {
		return nil, fmt.Errorf("sysproxy: state file %s exists; run acd-ctx attach --restore first", paths[0])
	}
	b, err := Select(e)
	if err != nil {
		return nil, err
	}
	desired, err := b.Plan(ctx, r, ep)
	if err != nil {
		return nil, err
	}
	prev := make([]Setting, 0, len(desired))
	for _, d := range desired {
		p, err := b.Read(ctx, r, d.Key)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", d.Key, err)
		}
		prev = append(prev, p)
	}
	pid := os.Getpid()
	st := &State{
		Mode: ModeSystemProxy, Backend: b.Name(), PID: pid,
		StartTime: startTime(e.procRoot(), pid), Created: e.now(),
		Applied: desired, Previous: prev,
	}
	path, err := createState(dir, st)
	if err != nil {
		return nil, err
	}
	a := &Active{env: e, mode: ModeSystemProxy, state: st, path: path, back: b}

	if err := writeSettings(ctx, r, b, desired); err != nil {
		return nil, a.rollback(err)
	}
	for _, d := range desired {
		cur, err := b.Read(ctx, r, d.Key)
		if err != nil {
			return nil, a.rollback(fmt.Errorf("verify %s: %w", d.Key, err))
		}
		if !same(cur, d) {
			return nil, a.rollback(fmt.Errorf("verify %s: value did not take effect", d.Key))
		}
	}
	return a, nil
}

// writeSettings runs the commands of every setting in order.
func writeSettings(ctx context.Context, r Runner, b Backend, settings []Setting) error {
	for _, s := range settings {
		cmds, err := b.Write(s)
		if err != nil {
			return err
		}
		for _, c := range cmds {
			if _, err := r.Run(ctx, c); err != nil {
				return fmt.Errorf("%s: %w", c, err)
			}
		}
	}
	return nil
}

// rollback restores after a failed start. The state file stays when the
// restore itself fails, so attach --restore can finish the job.
func (a *Active) rollback(cause error) error {
	ctx, cancel := RestoreContext()
	defer cancel()
	_, err := a.Stop(ctx)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("rollback: %w", err))
	}
	return cause
}

// restoreFirewall dispatches the restore of a transparent state by its tool.
func restoreFirewall(ctx context.Context, r Runner, st *State) ([]string, error) {
	switch st.Firewall {
	case "", FirewallNFT:
		return restoreNFT(ctx, r, st.NFTTable)
	case FirewallIptables:
		return restoreIptables(ctx, r, st)
	}
	return nil, fmt.Errorf("sysproxy: unknown redirect tool %q", st.Firewall)
}

// Stop restores what this run changed, verifies it, and removes the state file.
// It is idempotent. Pass RestoreContext, because the run's own context may
// already be canceled.
func (a *Active) Stop(ctx context.Context) ([]string, error) {
	if a.done {
		return nil, nil
	}
	r, err := a.env.runner()
	if err != nil {
		return nil, err
	}
	var notes []string
	switch a.mode {
	case ModeSystemProxy:
		notes, err = restoreSystem(ctx, r, a.back, a.state)
	case ModeTransparent:
		notes, err = restoreFirewall(ctx, r, a.state)
	}
	if err != nil {
		return notes, err
	}
	if a.path != "" {
		if rerr := removeFile(a.path); rerr != nil {
			return notes, rerr
		}
	}
	// Only a verified restore with its state file removed lets the helper
	// stand down. Before that it stays, and it restores if this run dies.
	if err := a.dog.standDown(); err != nil {
		notes = append(notes, "watchdog: "+err.Error())
	}
	a.done = true
	return notes, nil
}

// restoreSystem puts back each previous value that still holds what this run
// set. A value that changed since then belongs to someone else and is left
// alone, with a note. Every restored value is read again to verify it.
func restoreSystem(ctx context.Context, r Runner, b Backend, st *State) ([]string, error) {
	prev := make(map[string]Setting, len(st.Previous))
	for _, p := range st.Previous {
		prev[p.Key] = p
	}
	var notes []string
	var errs []error
	for _, a := range st.Applied {
		cur, err := b.Read(ctx, r, a.Key)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", a.Key, err))
			continue
		}
		if !same(cur, a) {
			notes = append(notes, fmt.Sprintf("%s changed by another program since acd-ctx set it; left as is", a.Key))
			continue
		}
		p, ok := prev[a.Key]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: no saved previous value", a.Key))
			continue
		}
		if err := writeSettings(ctx, r, b, []Setting{p}); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", a.Key, err))
			continue
		}
		back, err := b.Read(ctx, r, a.Key)
		if err != nil {
			errs = append(errs, fmt.Errorf("verify %s: %w", a.Key, err))
			continue
		}
		if !same(back, p) {
			errs = append(errs, fmt.Errorf("verify %s: previous value not restored", a.Key))
			continue
		}
		notes = append(notes, fmt.Sprintf("restored %s", a.Key))
	}
	return notes, errors.Join(errs...)
}
