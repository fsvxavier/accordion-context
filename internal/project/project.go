// Package project resolves the project a working directory belongs to.
//
// Precedence while walking upward from cwd (first match wins):
//  1. <dir>/.acd-ctx directory (local mode, ID read from .acd-ctx/project-id)
//  2. <dir>/.git (directory or file, covers worktrees and submodules)
//  3. <dir>/<marker> for any configured marker (go.mod, package.json, ...)
//  4. cwd itself when nothing matches.
package project

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Resolved describes the project found for a working directory.
type Resolved struct {
	ID      string
	Root    string
	Storage string // "global" | "local"
	Source  string // "local-dir" | "git" | "marker" | "cwd"
}

const (
	localDirName    = ".acd-ctx"
	projectIDName   = "project-id"
	globalIDBytes   = 8
	storageGlobal   = "global"
	storageLocal    = "local"
	sourceLocalDir  = "local-dir"
	sourceGit       = "git"
	sourceMarker    = "marker"
	sourceCWD       = "cwd"
	localIDRandSize = 8
)

// Options tunes ResolveWith.
type Options struct {
	// ReadOnly forbids writes. A local project without .acd-ctx/project-id is
	// not given one; it gets the ID derived from its root path instead.
	ReadOnly bool
}

// Resolve finds the project root for cwd. markers are file names (e.g.
// "go.mod") whose presence marks a project root when no .acd-ctx or .git
// exists at the same or a closer level.
func Resolve(cwd string, markers []string) (Resolved, error) {
	return ResolveWith(cwd, markers, Options{})
}

// ResolveWith is Resolve with options. With ReadOnly set it never writes to
// the file system.
func ResolveWith(cwd string, markers []string, opts Options) (Resolved, error) {
	dir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve cwd: %w", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return Resolved{}, fmt.Errorf("abs cwd: %w", err)
	}

	for d := dir; ; d = filepath.Dir(d) {
		localDir := filepath.Join(d, localDirName)
		if fi, err := os.Stat(localDir); err == nil && fi.IsDir() {
			id, err := localID(localDir, opts.ReadOnly)
			if err != nil {
				return Resolved{}, err
			}
			if id == "" { // read-only and no project-id yet
				return globalProject(d, sourceLocalDir), nil
			}
			return Resolved{ID: id, Root: d, Storage: storageLocal, Source: sourceLocalDir}, nil
		}
		if exists(filepath.Join(d, ".git")) {
			return globalProject(d, sourceGit), nil
		}
		for _, m := range markers {
			if m != "" && exists(filepath.Join(d, m)) {
				return globalProject(d, sourceMarker), nil
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break // filesystem root reached
		}
	}
	return globalProject(dir, sourceCWD), nil
}

// localID reads <localDir>/project-id. When the file is missing it is created
// with a random ID so that the project identity survives moves and renames.
// With readOnly set, a missing file yields "" and nothing is written.
func localID(localDir string, readOnly bool) (string, error) {
	path := filepath.Join(localDir, projectIDName)
	b, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(b))
		if id == "" {
			return "", fmt.Errorf("%s is empty", path)
		}
		return id, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read project id: %w", err)
	}
	if readOnly {
		return "", nil
	}
	buf := make([]byte, localIDRandSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random project id: %w", err)
	}
	id := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write project id: %w", err)
	}
	return id, nil
}

// globalProject derives a stable ID from the absolute root path.
func globalProject(root, source string) Resolved {
	sum := sha256.Sum256([]byte(root))
	return Resolved{
		ID:      hex.EncodeToString(sum[:globalIDBytes]),
		Root:    root,
		Storage: storageGlobal,
		Source:  source,
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
