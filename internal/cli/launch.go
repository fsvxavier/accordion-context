package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fsvxavier/accordion-context/internal/launcher"
	"github.com/fsvxavier/accordion-context/internal/plugin"
)

// runClient handles "claude|codex|gemini|opencode [--] [args]": the client
// name is the command, and its profile is the built-in one for that name.
func runClient(name string, args []string, s IO) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	prof, ok := launcher.ProfileFor(name)
	if !ok {
		return fail(s, fmt.Errorf("no client profile for %q", name))
	}
	return launch(s, append([]string{name}, args...), &prof)
}

// runGeneric handles "run -- <cmd> [args]". The profile is looked up from the
// command name; an unknown command is left to the launcher to refuse.
func runGeneric(args []string, s IO) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return usageError(s, "usage: run -- <cmd> [args]")
	}
	var prof *launcher.Profile
	if p, ok := launcher.ProfileFor(args[0]); ok {
		prof = &p
	}
	return launch(s, args, prof)
}

func launch(s IO, args []string, prof *launcher.Profile) int {
	code, err := launcher.Run(s.Ctx, launcher.Options{
		Args:     args,
		Profile:  prof,
		Cwd:      s.Cwd,
		Version:  s.Version,
		Stdin:    s.Stdin,
		Stdout:   s.Stdout,
		Stderr:   s.Stderr,
		ProxyLog: s.Stderr,
	})
	if err != nil {
		fmt.Fprintf(s.Stderr, "acd-ctx: %v\n", err)
		if code == ExitOK {
			code = ExitFail
		}
	}
	return code
}

// runMCP serves the companion MCP server on stdin/stdout (spec §19.3, item 5).
// Stdout carries the protocol, so every diagnostic goes to stderr.
func runMCP(args []string, s IO) int {
	if len(args) > 0 {
		return usageError(s, "usage: mcp")
	}
	sc, err := resolveScope(s)
	if err != nil {
		return fail(s, err)
	}
	st, err := openStore(sc.dbPath)
	if err != nil {
		return fail(s, err)
	}
	defer st.Close()
	logger := slog.New(slog.NewTextHandler(s.Stderr, nil))
	m, err := plugin.NewMCP(plugin.MCPConfig{
		Store:   st,
		DBPath:  sc.dbPath,
		Cwd:     s.Cwd,
		Version: s.Version,
		Logger:  logger,
	})
	if err != nil {
		return fail(s, err)
	}
	defer m.Close()
	err = m.Serve(s.Ctx, s.Stdin, s.Stdout)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fail(s, err)
	}
	return ExitOK
}
