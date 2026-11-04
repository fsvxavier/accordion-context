package sysproxy

// owner reports whether pid is alive and, when start is known, still has the
// same start time. Start times are read from procRoot, so a fake root works in
// tests.
func owner(e Env, pid int, start uint64) bool {
	if !signalAlive(pid) {
		return false
	}
	if start == 0 {
		return true
	}
	cur := startTime(e.procRoot(), pid)
	return cur == 0 || cur == start
}
