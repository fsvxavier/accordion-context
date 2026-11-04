//go:build !unix

package sysproxy

// signalAlive cannot check other processes here, so it reports every pid as
// alive. Stale state is then kept until the user removes it by hand, and is
// never restored under a live owner.
func signalAlive(pid int) bool { return pid > 0 }
