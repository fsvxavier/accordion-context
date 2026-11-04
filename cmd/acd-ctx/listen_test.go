package main

import (
	"bytes"
	"testing"
)

// TestServeRefusesNonLoopbackWithoutOptIn checks that serve refuses before it
// opens the store: no flags, env or allowlist combination binds a wildcard
// address unless --allow-non-loopback and a non-empty --allowed-hosts are given.
func TestServeRefusesNonLoopbackWithoutOptIn(t *testing.T) {
	t.Setenv("ACD_CTX_ALLOW_NON_LOOPBACK", "")
	t.Setenv("ACD_CTX_ALLOWED_HOSTS", "")
	dir := t.TempDir()
	cases := [][]string{
		{"serve", "--listen", "0.0.0.0:0", "--db", dir + "/a.db"},
		{"serve", "--listen", "0.0.0.0:0", "--allow-non-loopback", "--db", dir + "/a.db"},
		{"serve", "--listen", "0.0.0.0:0", "--allowed-hosts", "localhost", "--db", dir + "/a.db"},
		{"serve", "--listen", "0.0.0.0:0", "--allow-non-loopback", "--allowed-hosts", "evil.com/*", "--db", dir + "/a.db"},
	}
	for _, args := range cases {
		var stderr bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2; stderr: %s", args, code, stderr.String())
		}
	}
}
