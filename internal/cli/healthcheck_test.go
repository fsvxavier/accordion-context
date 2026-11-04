package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func healthServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__acd-ctx/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthCheck(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"healthy", http.StatusOK, `{"ok":true}`, ExitOK},
		{"not ok", http.StatusOK, `{"ok":false}`, ExitFail},
		{"server error", http.StatusInternalServerError, `{"ok":true}`, ExitFail},
		{"not json", http.StatusOK, `<html>`, ExitFail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := healthServer(t, tc.status, tc.body)
			var stderr bytes.Buffer
			if got := healthCheck(context.Background(), srv.URL+"/__acd-ctx/health", &stderr); got != tc.want {
				t.Fatalf("exit %d, want %d (stderr %q)", got, tc.want, stderr.String())
			}
		})
	}
}

func TestHealthCheckUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // nothing listens any more
	var stderr bytes.Buffer
	if got := healthCheck(context.Background(), addr+"/__acd-ctx/health", &stderr); got != ExitFail {
		t.Fatalf("exit %d for an unreachable proxy, want %d", got, ExitFail)
	}
}

func TestRunHealthcheckPort(t *testing.T) {
	srv := healthServer(t, http.StatusOK, `{"ok":true}`)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	streams := IO{Ctx: context.Background(), Stderr: &stderr}
	if code := runHealthcheck([]string{"--port", u.Port()}, streams); code != ExitOK {
		t.Fatalf("runHealthcheck exit %d, stderr %q", code, stderr.String())
	}
	if code := runHealthcheck([]string{"--port", "notanumber"}, streams); code != ExitUsage {
		t.Fatalf("bad port exit %d, want usage", code)
	}
	// The subcommand is reachable through the public dispatcher as well.
	if code := Run([]string{"healthcheck", "--port", u.Port()}, streams); code != ExitOK {
		t.Fatalf("Run(healthcheck) exit %d, stderr %q", code, stderr.String())
	}
}
