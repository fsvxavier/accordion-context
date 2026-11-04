// Package cli implements the acd-ctx command line (spec §18.7, §19.8). Every
// command is a function of its arguments, its streams and its environment, so
// tests drive it without a process. cmd/acd-ctx calls Run.
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// Exit codes. ExitUsage is for malformed command lines.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

// IO carries the streams and environment of one invocation. Zero fields take
// the process defaults; Stdin, Stdout and Stderr default to empty or discarded
// streams, so pass os.Stdin and friends from main.
type IO struct {
	// Ctx stops long-running commands (mcp). Nil means context.Background.
	Ctx context.Context
	// Stdin is read by confirmations and by the MCP server.
	Stdin io.Reader
	// Stdout carries command output and the MCP protocol.
	Stdout io.Writer
	// Stderr carries diagnostics, warnings, prompts and client logs.
	Stderr io.Writer
	// LookupEnv reads environment variables (ACD_CTX_*, XDG_*, HOME). Nil
	// means os.LookupEnv.
	LookupEnv func(key string) (string, bool)
	// Cwd is the working directory used for project detection. Empty means
	// the process working directory.
	Cwd string
	// Version is reported to the proxy and to MCP clients.
	Version string
}

func (s IO) withDefaults() (IO, error) {
	if s.Ctx == nil {
		s.Ctx = context.Background()
	}
	if s.Stdin == nil {
		s.Stdin = strings.NewReader("")
	}
	if s.Stdout == nil {
		s.Stdout = io.Discard
	}
	if s.Stderr == nil {
		s.Stderr = io.Discard
	}
	if s.LookupEnv == nil {
		s.LookupEnv = os.LookupEnv
	}
	if s.Cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return s, fmt.Errorf("working directory: %w", err)
		}
		s.Cwd = wd
	}
	return s, nil
}

// Run executes one acd-ctx command line (args without the program name) and
// returns the process exit code.
func Run(args []string, streams IO) int {
	s, err := streams.withDefaults()
	if err != nil {
		fmt.Fprintf(streamOrDiscard(streams.Stderr), "acd-ctx: %v\n", err)
		return ExitFail
	}
	if len(args) == 0 {
		fmt.Fprint(s.Stderr, Usage())
		return ExitUsage
	}
	switch args[0] {
	case "project":
		return runProject(args[1:], s)
	case "config":
		return runConfig(args[1:], s)
	case "claude", "codex", "gemini", "opencode":
		return runClient(args[0], args[1:], s)
	case "run":
		return runGeneric(args[1:], s)
	case "mcp":
		return runMCP(args[1:], s)
	case "help", "-h", "--help":
		fmt.Fprint(s.Stdout, Usage())
		return ExitOK
	}
	fmt.Fprintf(s.Stderr, "acd-ctx: unknown command %q\n\n%s", args[0], Usage())
	return ExitUsage
}

func streamOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// Usage describes the command line.
func Usage() string {
	return `usage: acd-ctx <command> [args]

project init [--local-storage]        create .acd-ctx/ at the project root
project show                          detected project, root, storage and database
project list                          projects in the global database
project move <old> <new>              re-point a moved project's root path
project migrate --to local|global     copy the project's sessions between databases
project purge <id> [--yes]            delete all data of a project
config show --effective               effective configuration with the source of each key
claude|codex|gemini|opencode [args]   launch a client through the proxy
run -- <cmd> [args]                   launch any client whose profile is known
mcp                                   serve the companion MCP server on stdio
help                                  show this help

Environment: ACD_CTX_DB overrides the global database path.
`
}

// Default paths (spec §18.1). The global database honours XDG_DATA_HOME.
const (
	dbFileName      = "acd-ctx.db"
	localDirName    = ".acd-ctx"
	projectIDName   = "project-id"
	configFileName  = "config.yaml"
	dirMode         = 0o700
	projectIDMode   = 0o644
	configFileMode  = 0o644
	ignoreFileMode  = 0o644
	ignoreFileText  = "acd-ctx.db*\nexports/\n"
	projectHashSize = 8
)

var projectMarkers = []string{"go.mod", "package.json", "pyproject.toml", "composer.json", "Cargo.toml"}

// globalDBPath is the global database: ACD_CTX_DB, else
// $XDG_DATA_HOME/acd-ctx/acd-ctx.db, else ~/.local/share/acd-ctx/acd-ctx.db.
func globalDBPath(lookup func(string) (string, bool)) (string, error) {
	if v, ok := lookup("ACD_CTX_DB"); ok && strings.TrimSpace(v) != "" {
		return v, nil
	}
	if v, ok := lookup("XDG_DATA_HOME"); ok && v != "" {
		return filepath.Join(v, "acd-ctx", dbFileName), nil
	}
	home, err := homeDir(lookup)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "acd-ctx", dbFileName), nil
}

// globalConfigPath is the global config file (spec §18.1). On Linux it follows
// the injected XDG_CONFIG_HOME or HOME; other systems use the OS convention.
func globalConfigPath(lookup func(string) (string, bool)) (string, error) {
	if v, ok := lookup("XDG_CONFIG_HOME"); ok && v != "" {
		return filepath.Join(v, "acd-ctx", configFileName), nil
	}
	if runtime.GOOS == "linux" {
		home, err := homeDir(lookup)
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "acd-ctx", configFileName), nil
	}
	return config.DefaultGlobalPath()
}

func homeDir(lookup func(string) (string, bool)) (string, error) {
	if v, ok := lookup("HOME"); ok && v != "" {
		return v, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return h, nil
}

// scope is the project a command works on, with its effective configuration.
type scope struct {
	res    project.Resolved
	cfg    config.Result
	dbPath string // database that holds this project's data
}

// localDBPath is the database of a project in local storage (§18.3).
func localDBPath(root string) string {
	return filepath.Join(root, localDirName, dbFileName)
}

// resolveScope finds the project for the working directory without writing
// anything (read-only resolution), loads the configuration for it and picks
// the database from the effective "storage" key.
func resolveScope(s IO) (scope, error) {
	res, err := resolveProject(s)
	if err != nil {
		return scope{}, err
	}
	gp, err := globalConfigPath(s.LookupEnv)
	if err != nil {
		return scope{}, err
	}
	cfg, err := config.Load(config.Options{GlobalPath: gp, Root: res.Root, LookupEnv: s.LookupEnv})
	if err != nil {
		return scope{}, err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(s.Stderr, "acd-ctx: warning: %s\n", w)
	}
	sc := scope{res: res, cfg: cfg}
	if cfg.Config.Storage == "local" {
		sc.dbPath = localDBPath(res.Root)
	} else {
		sc.dbPath, err = globalDBPath(s.LookupEnv)
		if err != nil {
			return scope{}, err
		}
	}
	return sc, nil
}

// resolveProject resolves the project of s.Cwd read-only. A global project
// whose root is registered in the global database keeps that ID, so a project
// moved with `project move` stays attached to its history.
func resolveProject(s IO) (project.Resolved, error) {
	return project.ResolveWith(s.Cwd, projectMarkers, project.Options{
		ReadOnly: true,
		IDByRoot: func(root string) (string, bool) { return registeredID(s, root) },
	})
}

// registeredID looks root up in the global database, if one exists.
func registeredID(s IO, root string) (string, bool) {
	path, err := globalDBPath(s.LookupEnv)
	if err != nil || !fileExists(path) {
		return "", false
	}
	st, err := store.Open(path)
	if err != nil {
		return "", false
	}
	defer st.Close()
	id, err := st.ProjectIDByRoot(context.Background(), root)
	return id, err == nil
}

// openStore opens a database, creating its directory when needed.
func openStore(path string) (*store.Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	st, err := store.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return st, nil
}

// fileExists reports whether path exists (any type).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// globalProjectID is the ID a global-mode project gets from its root path
// (spec §18.2). It must match internal/project; the tests check that.
func globalProjectID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:projectHashSize])
}

// readLocalProjectID reads <root>/.acd-ctx/project-id.
func readLocalProjectID(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, localDirName, projectIDName))
	if err != nil {
		return "", fmt.Errorf("read project id: %w", err)
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("%s is empty", filepath.Join(root, localDirName, projectIDName))
	}
	return id, nil
}

// parseFlags parses fs with flags allowed before or after positional
// arguments. It returns the positional arguments.
func parseFlags(fset *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fset.Parse(args); err != nil {
			return nil, err
		}
		args = fset.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func newFlagSet(name string, s IO) *flag.FlagSet {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(s.Stderr)
	return fset
}

// usageError prints a usage message and returns ExitUsage.
func usageError(s IO, msg string) int {
	fmt.Fprintf(s.Stderr, "acd-ctx: %s\n\n%s", msg, Usage())
	return ExitUsage
}

// fail prints an error and returns ExitFail.
func fail(s IO, err error) int {
	fmt.Fprintf(s.Stderr, "acd-ctx: %v\n", err)
	return ExitFail
}

var errNoLocalProject = errors.New("project has no .acd-ctx/ directory; run acd-ctx project init first")
