// Command acd-ctx runs the Accordion Context proxy and reports its version.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fsvxavier/accordion-context/internal/cli"
	"github.com/fsvxavier/accordion-context/internal/plugin"
	"github.com/fsvxavier/accordion-context/internal/project"
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
		// project, config, mcp and the client launchers live in internal/cli.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return cli.Run(args, cli.IO{Ctx: ctx, Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, Version: version})
	}
}

func serve(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8787", "loopback address to listen on")
	upstream := fs.String("upstream", "https://api.anthropic.com", "default upstream API base URL, used when the request has no /ctx/<url>/ prefix")
	dbPath := fs.String("db", "", "SQLite database path (default ~/.local/share/acd-ctx/acd-ctx.db)")
	allow := fs.String("allow", "", "comma-separated origins reachable through /ctx/<url>/ (default: the --upstream origin)")
	configPath := fs.String("config", "", "global config file (default: the user config path, spec §18.1)")
	markers := fs.String("markers", "", "comma-separated project root markers (default: go.mod,package.json,pyproject.toml,composer.json,Cargo.toml)")
	pluginTokenFile := fs.String("plugin-token-file", "", "file that receives the plugin bearer token, mode 0600 (default ~/.local/share/acd-ctx/plugin.token, or plugin-<port>.token for a non-default --listen port)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var rootMarkers []string
	for _, m := range strings.Split(*markers, ",") {
		if m = strings.TrimSpace(m); m != "" {
			rootMarkers = append(rootMarkers, m)
		}
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
	tokenPath := *pluginTokenFile
	if tokenPath == "" {
		// Without a home directory the plugin protocol stays off (see below).
		if home, err := os.UserHomeDir(); err == nil {
			tokenPath = filepath.Join(home, ".local", "share", "acd-ctx", defaultTokenName(*listen))
		}
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	st, err := store.Open(path)
	if err != nil {
		logger.Error("open store", "err", err)
		return 1
	}
	defer st.Close()

	listenHost, _, _ := net.SplitHostPort(*listen)
	h, err := proxy.New(proxy.Config{Upstream: up, Store: st, Version: version, Logger: logger,
		UpstreamAllow: allowed, ListenHost: listenHost, GlobalPath: *configPath, Markers: rootMarkers})
	if err != nil {
		logger.Error("configure proxy", "err", err)
		return 1
	}

	// The plugin protocol (spec §19.4) shares the store and is mounted by prefix,
	// without stripping it. It is optional: when its setup fails, the proxy keeps
	// serving alone (fail-open, spec §19.7).
	root := http.Handler(h)
	if ph, err := func() (*plugin.Handler, error) {
		if tokenPath == "" {
			return nil, errors.New("no token path: set --plugin-token-file")
		}
		token, err := plugin.NewToken()
		if err != nil {
			return nil, err
		}
		if err := plugin.WriteTokenFile(tokenPath, token); err != nil {
			return nil, fmt.Errorf("write token file: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		projMarkers := rootMarkers
		if projMarkers == nil {
			projMarkers = proxy.ProjectMarkers()
		}
		proj, err := project.ResolveWith(cwd, projMarkers, project.Options{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		return plugin.New(plugin.Config{Store: st, Project: proj, Token: token, ListenHost: listenHost,
			Version: version, Logger: logger})
	}(); err != nil {
		logger.Warn("plugin protocol disabled; serving the proxy only", "err", err)
	} else {
		root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/__acd-ctx/plugin/") {
				ph.ServeHTTP(w, r)
				return
			}
			h.ServeHTTP(w, r)
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("serving", "listen", *listen, "upstream_host", up.Host, "db", path, "plugin_token_file", tokenPath)
	if err := proxy.Run(ctx, *listen, root); err != nil {
		logger.Error("serve", "err", err)
		return 1
	}
	logger.Info("stopped")
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: acd-ctx <command>

commands:
  serve     run the compaction proxy (--listen, --upstream, --db, --allow, --config, --markers)
  version   print the version

Other commands:`)
	fmt.Fprint(w, cli.Usage())
}

// defaultTokenName is plugin.token for the default port and plugin-<port>.token
// otherwise, so two serve instances on one machine do not overwrite each other's token.
func defaultTokenName(listen string) string {
	if _, port, err := net.SplitHostPort(listen); err == nil && port != "" && port != "8787" {
		return "plugin-" + port + ".token"
	}
	return "plugin.token"
}
