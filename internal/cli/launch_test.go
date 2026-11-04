package cli

import (
	"io"
	"net"
	"net/http"
	"os"
	"testing"
)

// TestMain lets the test binary stand in for "acd-ctx serve": the launcher
// starts its proxy by running os.Executable() with "serve --listen <addr>".
func TestMain(m *testing.M) {
	if os.Getenv("CLI_TEST_FAKE_SERVE") == "1" {
		os.Exit(fakeServe(os.Args))
	}
	os.Exit(m.Run())
}

// fakeServe answers the health and status endpoints the launcher polls, with
// the version the fixture uses ("test").
func fakeServe(args []string) int {
	for i := range args {
		if args[i] != "--listen" || i+1 >= len(args) {
			continue
		}
		ln, err := net.Listen("tcp", args[i+1])
		if err != nil {
			return 1
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/__acd-ctx/health", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"ok":true}`)
		})
		mux.HandleFunc("/__acd-ctx/status", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"version":"test"}`)
		})
		if err := http.Serve(ln, mux); err != nil {
			return 1
		}
		return 0
	}
	return 2
}
