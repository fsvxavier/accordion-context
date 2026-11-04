//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package mitm

// lockCADir is a no-op where flock is unavailable. Concurrent processes on
// such platforms can still interleave CA writes; run one acd-ctx at a time.
func lockCADir(string) (func(), error) { return func() {}, nil }
