package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

// healthTimeout bounds the whole probe, so a hung proxy fails the check quickly.
const healthTimeout = 3 * time.Second

// runHealthcheck implements `acd-ctx healthcheck [--port N]`. It is meant for
// container HEALTHCHECK instructions, where no shell or curl is available: it
// GETs the proxy health endpoint on 127.0.0.1 and exits 0 only on a 200 with ok=true.
func runHealthcheck(args []string, s IO) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(s.Stderr)
	port := fs.Int("port", 8787, "proxy port on 127.0.0.1")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	target := fmt.Sprintf("http://127.0.0.1:%d/__acd-ctx/health", *port)
	return healthCheck(s.Ctx, target, s.Stderr)
}

// healthCheck probes target and reports the outcome on stderr.
func healthCheck(ctx context.Context, target string, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return ExitFail
	}
	resp, err := (&http.Client{Timeout: healthTimeout}).Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return ExitFail
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: %s answered %s\n", target, resp.Status)
		return ExitFail
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil || !body.OK {
		fmt.Fprintf(stderr, "healthcheck: %s did not report ok\n", target)
		return ExitFail
	}
	return ExitOK
}
