// Command acd-ctx runs the Accordion Context proxy and reports its version.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fsvxavier/accordion-context/internal/proxy"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stderr)
	case "version":
		fmt.Fprintln(stdout, "acd-ctx", version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "acd-ctx: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func serve(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8787", "loopback address to listen on")
	upstream := fs.String("upstream", "https://api.anthropic.com", "upstream Messages API base URL")
	dbPath := fs.String("db", "", "SQLite database path (default ~/.local/share/acd-ctx/acd-ctx.db)")
	allow := fs.String("allow", "", "comma-separated origins reachable through /ctx/<url>/ (default: the --upstream origin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var allowed []string
	for _, a := range strings.Split(*allow, ",") {
		if a = strings.TrimSpace(a); a != "" {
			allowed = append(allowed, a)
		}
	}

	if err := proxy.CheckLoopback(*listen); err != nil {
		fmt.Fprintln(stderr, "acd-ctx serve:", err)
		return 2
	}
	up, err := url.Parse(*upstream)
	if err != nil {
		fmt.Fprintln(stderr, "acd-ctx serve: --upstream:", err)
		return 2
	}
	path := *dbPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "acd-ctx serve: --db is required without a home directory:", err)
			return 1
		}
		path = filepath.Join(home, ".local", "share", "acd-ctx", "acd-ctx.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintln(stderr, "acd-ctx serve: create db directory:", err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	st, err := store.Open(path)
	if err != nil {
		logger.Error("open store", "err", err)
		return 1
	}
	defer st.Close()

	h, err := proxy.New(proxy.Config{Upstream: up, Store: st, Version: version, Logger: logger, UpstreamAllow: allowed})
	if err != nil {
		logger.Error("configure proxy", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("serving", "listen", *listen, "upstream_host", up.Host, "db", path)
	if err := proxy.Run(ctx, *listen, h); err != nil {
		logger.Error("serve", "err", err)
		return 1
	}
	logger.Info("stopped")
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: acd-ctx <command>

commands:
  serve     run the compaction proxy (--listen, --upstream, --db, --allow)
  version   print the version`)
}
