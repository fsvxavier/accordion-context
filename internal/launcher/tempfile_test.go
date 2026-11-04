package launcher

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestWriteTempConfigModeAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	tc := TempConfig{PathEnv: "X_CONFIG", Format: "json", Template: `{"u":"{url}"}`}
	path, err := WriteTempConfig(dir, tc, "http://127.0.0.1:1/p/a", "1", "tok")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "http://127.0.0.1:1/p/a") {
		t.Fatalf("content = %s", b)
	}
	if _, err := WriteTempConfig(dir, tc, "http://x", "1", "tok"); err == nil {
		t.Fatal("second write over an existing file succeeded")
	}
}
