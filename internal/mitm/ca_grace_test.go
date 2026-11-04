package mitm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoadOrCreateCAConcurrentProcessesAgree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	const n = 8
	pems := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ca, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"api.example.test"}})
			errs[i] = err
			if err == nil {
				pems[i] = ca.certPEM
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if !bytes.Equal(pems[0], pems[i]) {
			t.Fatalf("call %d generated a different CA; key/cert writes interleaved", i)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, caLockName)); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestReplacedCAStaysInBundleDuringGrace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	first, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"a.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(mustBundle(t, first)), "BEGIN CERTIFICATE"); n != 1 {
		t.Fatalf("fresh CA bundle has %d certs, want 1", n)
	}
	// A constraint change forces regeneration; the old cert is kept as previous.
	second, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"b.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.certPEM, second.certPEM) {
		t.Fatal("constraint change did not regenerate the CA")
	}
	if n := strings.Count(string(mustBundle(t, second)), "BEGIN CERTIFICATE"); n != 2 {
		t.Fatalf("bundle after regeneration has %d certs, want new + previous", n)
	}
	// Reloading within the grace period keeps the previous cert.
	again, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"b.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(mustBundle(t, again)), "BEGIN CERTIFICATE"); n != 2 {
		t.Fatalf("reload bundle has %d certs, want 2", n)
	}
}

func TestPreviousCAExpiresAfterGrace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if _, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"a.example.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"b.example.test"}}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-PrevCAGrace - time.Hour)
	if err := os.Chtimes(filepath.Join(dir, caPrevName), old, old); err != nil {
		t.Fatal(err)
	}
	ca, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"b.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(mustBundle(t, ca)), "BEGIN CERTIFICATE"); n != 1 {
		t.Fatalf("bundle past grace has %d certs, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(dir, caPrevName)); !os.IsNotExist(err) {
		t.Fatalf("stale previous cert not removed: %v", err)
	}
}

func mustBundle(t *testing.T, ca *CA) []byte {
	t.Helper()
	b, err := ca.Bundle(false)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
